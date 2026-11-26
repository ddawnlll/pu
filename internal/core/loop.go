package core

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ddawnlll/pu/internal/catalog"
	"github.com/ddawnlll/pu/internal/edit"
	"github.com/ddawnlll/pu/internal/plan"
	"github.com/ddawnlll/pu/internal/provider"
	"github.com/ddawnlll/pu/internal/store"
)

//go:embed system.txt
var SystemPrompt string

const maxOpenStepContinuations = 2

// Streamer is the minimal provider interface needed by the core loop.
type Streamer interface {
	Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error)
}

// Status is the terminal outcome of a run.
type Status string

const (
	StatusCompleted       Status = "completed"
	StatusBlocked         Status = "blocked"
	StatusBudgetExhausted Status = "budget_exhausted"
	StatusInterrupted     Status = "interrupted"
	StatusFailed          Status = "failed"
)

// Ablation holds optional hidden toggles for M5 ablation experiments.
type Ablation struct {
	NoTurn0        bool
	NoHashline     bool
	NoPinnedPrefix bool
	NoStaged       bool
}

// ParseAblate parses a comma-separated list of ablation flags:
// turn0, hashline, pinned-prefix, staged.
func ParseAblate(raw string) Ablation {
	var a Ablation
	for _, tok := range strings.Split(raw, ",") {
		switch strings.TrimSpace(strings.ToLower(tok)) {
		case "turn0":
			a.NoTurn0 = true
		case "hashline":
			a.NoHashline = true
		case "pinned-prefix":
			a.NoPinnedPrefix = true
		case "staged":
			a.NoStaged = true
		}
	}
	return a
}

// Config configures one headless agent run.
type Config struct {
	RepoRoot       string
	SessionID      string
	Model          catalog.Model
	MaxCostUSD     float64
	MaxTurns       int
	Timeout        time.Duration
	ProcessStart   time.Time // optional process start timestamp so WallTimeMS covers full CLI startup
	UntrustedRepo  bool
	TrustUntrusted bool
	Ablate         Ablation
	Journal        *store.Journal
	OnStreamEvent  func(provider.StreamEvent)
}

// Report is the machine-readable summary emitted at the end of a run.
type Report struct {
	Status          Status          `json:"status"`
	SessionID       string          `json:"session_id"`
	Model           string          `json:"model"`
	Turns           int             `json:"turns"`
	Tokens          provider.Usage  `json:"tokens"`
	NotionalCostUSD float64         `json:"notional_cost_usd"`
	WallTimeMS      int64           `json:"wall_time_ms"`
	FilesChanged    []string        `json:"files_changed,omitempty"`
	Evidence        []plan.Evidence `json:"evidence,omitempty"`
	FinalText       string          `json:"final_text,omitempty"`
	Reason          string          `json:"reason,omitempty"`
}

// Run executes the single-writer agent loop with a 4-layer KV-invariant prefix,
// parallel independent tool execution via errgroup, 64 KiB output spill,
// external-content exec gate, 2-stage KV-friendly context compaction,
// revision-locked evidence verification, and strict budget enforcement.
func Run(ctx context.Context, cfg Config, streamer Streamer, userPrompt string) (*Report, error) {
	startTime := cfg.ProcessStart
	if startTime.IsZero() {
		startTime = time.Now()
	}

	if cfg.RepoRoot == "" {
		cfg.RepoRoot = "."
	}
	if cfg.SessionID == "" {
		cfg.SessionID = fmt.Sprintf("pu-%d", startTime.UnixNano())
	}

	var runCtx context.Context
	var cancel context.CancelFunc
	if cfg.Timeout > 0 {
		effectiveTimeout := cfg.Timeout
		if effectiveTimeout > 4*time.Second {
			// Reserve a 2s internal cushion so pu always finishes and flushes its JSON report.
			effectiveTimeout -= 2 * time.Second
		}
		runCtx, cancel = context.WithTimeout(ctx, effectiveTimeout)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	// Open append-only journal if not supplied by caller.
	journal := cfg.Journal
	var ownJournal bool
	if journal == nil {
		var err error
		journal, err = store.Open(store.SessionPath(cfg.RepoRoot, cfg.SessionID))
		if err != nil {
			return nil, fmt.Errorf("open session journal: %w", err)
		}
		ownJournal = true
		defer journal.Close()
	}

	logEvent := func(eventType string, payload any) {
		if journal != nil {
			_, _ = journal.Append(eventType, payload)
		}
	}

	// Build immutable Layers 1, 2, 3 once at session start.
	sysText := strings.TrimSpace(SystemPrompt)
	if cfg.Ablate.NoStaged {
		sysText = strings.ReplaceAll(sysText, "After a change, run the fastest check that covers it: build or lint first, then the targeted test. Run the full suite only when a shared interface changed or before finishing a multi-file change.\n", "")
	}
	layer1 := provider.Item{
		Role:  provider.RoleSystem,
		Parts: []provider.ContentPart{provider.TextPart{Text: sysText}},
	}
	layer2Tools := ToolDefs()
	projectRules := LoadProjectRules(cfg.RepoRoot)

	prefixItems := make([]provider.Item, 0, 2)
	prefixItems = append(prefixItems, layer1)
	if projectRules != "" {
		prefixItems = append(prefixItems, provider.Item{
			Role:  provider.RoleSystem,
			Parts: []provider.ContentPart{provider.TextPart{Text: projectRules}},
		})
	}
	prefixCount := len(prefixItems)

	// Turn-0 fingerprint goes into the first user turn (Layer 4), never system prompt.
	firstUserText := userPrompt
	if !cfg.Ablate.NoTurn0 {
		fingerprint := BuildTurn0Fingerprint(runCtx, cfg.RepoRoot)
		firstUserText = fingerprint + "\n\n" + userPrompt
	}

	history := make([]provider.Item, 0, 16)
	history = append(history, prefixItems...)
	history = append(history, provider.Item{
		Role:  provider.RoleUser,
		Parts: []provider.ContentPart{provider.TextPart{Text: firstUserText}},
	})

	tracker := edit.NewTracker(cfg.RepoRoot)
	checkpointer := edit.NewCheckpointer(cfg.RepoRoot, cfg.SessionID)
	planMgr := plan.NewManager(cfg.RepoRoot)
	toolMeta := make(map[string]toolResultMeta)
	var mutMu sync.Mutex // serializes state-mutating tools (patch, exec, plan)

	untrustedTainted := cfg.UntrustedRepo

	logEvent("session_start", map[string]any{
		"session_id":     cfg.SessionID,
		"model":          cfg.Model.ID,
		"repo_root":      cfg.RepoRoot,
		"untrusted_repo": cfg.UntrustedRepo,
	})

	var totalUsage provider.Usage
	var lastInputTokens int
	var totalCost float64
	turns := 0
	continuations := 0

	finish := func(status Status, finalText, reason string) *Report {
		planMgr.SetStatus(string(status))
		snap := planMgr.Snapshot()
		rep := &Report{
			Status:          status,
			SessionID:       cfg.SessionID,
			Model:           cfg.Model.ID,
			Turns:           turns,
			Tokens:          totalUsage,
			NotionalCostUSD: totalCost,
			WallTimeMS:      time.Since(startTime).Milliseconds(),
			FilesChanged:    checkpointer.ChangedFiles(),
			Evidence:        snap.Evidence,
			FinalText:       finalText,
			Reason:          reason,
		}
		logEvent("session_end", rep)
		if ownJournal && journal != nil {
			_ = journal.Close()
			journal = nil
		}
		return rep
	}

	for {
		// Pre-turn budget & cancellation checks.
		if err := runCtx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return finish(StatusBudgetExhausted, "", "timeout exceeded"), nil
			}
			return finish(StatusInterrupted, "", "context canceled"), nil
		}
		if cfg.MaxTurns > 0 && turns >= cfg.MaxTurns {
			return finish(StatusBudgetExhausted, "", fmt.Sprintf("max turns (%d) reached", cfg.MaxTurns)), nil
		}
		if cfg.MaxCostUSD > 0 && totalCost >= cfg.MaxCostUSD {
			return finish(StatusBudgetExhausted, "", fmt.Sprintf("max notional cost ($%.4f) reached", cfg.MaxCostUSD)), nil
		}

		// 2-stage KV-friendly context ceiling check (only mutates history at >=75% or >=85%).
		if turns > 0 && cfg.Model.ContextWindow > 0 {
			var stage int
			history, stage = compactHistory(
				history, prefixCount, cfg.Model.ContextWindow, lastInputTokens,
				toolMeta, tracker, planMgr, checkpointer, userPrompt,
			)
			if stage > 0 {
				logEvent("context_compaction", map[string]any{
					"turn":          turns,
					"stage":         stage,
					"history_items": len(history),
				})
				lastInputTokens = 0
			}
		}

		turns++
		reqItems := history
		if cfg.Ablate.NoPinnedPrefix {
			reqItems = append([]provider.Item(nil), history...)
			reqItems[0] = provider.Item{
				Role:  provider.RoleSystem,
				Parts: []provider.ContentPart{provider.TextPart{Text: fmt.Sprintf("%s\n[turn=%d ts=%d]", sysText, turns, time.Now().UnixNano())}},
			}
		}
		req := provider.Request{
			Model:           cfg.Model.ID,
			SessionID:       cfg.SessionID,
			Turn:            turns,
			Items:           reqItems,
			Tools:           layer2Tools,
			ReasoningParam:  cfg.Model.ReasoningParam,
			ReasoningEffort: cfg.Model.ReasoningEffort,
		}

		resp, err := streamer.Stream(runCtx, req, cfg.OnStreamEvent)
		if err != nil {
			if ctxErr := runCtx.Err(); ctxErr != nil {
				if errors.Is(ctxErr, context.DeadlineExceeded) {
					return finish(StatusBudgetExhausted, "", "timeout exceeded during model stream"), nil
				}
				return finish(StatusInterrupted, "", "canceled during model stream"), nil
			}
			return finish(StatusFailed, "", err.Error()), err
		}

		lastInputTokens = resp.Usage.InputTokens
		totalUsage.InputTokens += resp.Usage.InputTokens
		totalUsage.CachedInputTokens += resp.Usage.CachedInputTokens
		totalUsage.OutputTokens += resp.Usage.OutputTokens
		totalUsage.TotalTokens += resp.Usage.TotalTokens
		turnCost := cfg.Model.ComputeCostUSD(
			resp.Usage.InputTokens,
			resp.Usage.CachedInputTokens,
			resp.Usage.OutputTokens,
		)
		totalCost += turnCost

		logEvent("turn_usage", map[string]any{
			"turn":              turns,
			"usage":             resp.Usage,
			"notional_cost_usd": turnCost,
			"total_cost_usd":    totalCost,
			"finish_reason":     resp.FinishReason,
		})

		// Post-turn cost ceiling check: never report completed when over budget.
		if cfg.MaxCostUSD > 0 && totalCost > cfg.MaxCostUSD {
			return finish(StatusBudgetExhausted, "", fmt.Sprintf("max notional cost ($%.4f) exceeded ($%.4f)", cfg.MaxCostUSD, totalCost)), nil
		}

		var toolCalls []provider.ToolCallPart
		var textBuf strings.Builder
		for _, p := range resp.Item.Parts {
			switch v := p.(type) {
			case provider.ToolCallPart:
				toolCalls = append(toolCalls, v)
			case provider.TextPart:
				textBuf.WriteString(v.Text)
			}
		}

		history = append(history, resp.Item)

		if len(toolCalls) == 0 {
			finalText := strings.TrimSpace(textBuf.String())
			if planMgr.IsBlocked() {
				return finish(StatusBlocked, finalText, "plan marked blocked"), nil
			}
			if openSteps := planMgr.OpenSteps(); len(openSteps) > 0 {
				if continuations < maxOpenStepContinuations {
					continuations++
					names := make([]string, len(openSteps))
					for i, s := range openSteps {
						names[i] = fmt.Sprintf("%s (%s, status=%s)", s.ID, s.Title, s.Status)
					}
					contMsg := fmt.Sprintf(
						"Open plan steps remain without current evidence that covers the changed behaviour: %s. A passing check counts only if the same command failed earlier in this session (e.g. a test you added that failed before the fix), or it is the existing test named in the step's check field. Add or name such a test and run it on the current revision, or mark the plan blocked if you cannot proceed.",
						strings.Join(names, "; "),
					)
					logEvent("open_step_continuation", map[string]any{
						"turn":         turns,
						"continuation": continuations,
						"open_steps":   names,
					})
					history = append(history, provider.Item{
						Role:  provider.RoleUser,
						Parts: []provider.ContentPart{provider.TextPart{Text: contMsg}},
					})
					continue
				}
				return finish(StatusFailed, finalText, "open plan steps remain without current evidence after continuations"), nil
			}
			// Implicit gate when the model never made a plan: non-test code changed
			// but no passing check was shown to fail without the change.
			if !planMgr.HasPlan() && changedNonTest(checkpointer) && !planMgr.CoveredWithoutPlan() && continuations < maxOpenStepContinuations {
				continuations++
				logEvent("uncovered_change_continuation", map[string]any{"turn": turns, "continuation": continuations})
				history = append(history, provider.Item{
					Role: provider.RoleUser,
					Parts: []provider.ContentPart{provider.TextPart{Text: "No passing check yet covers your change: none of your green tests was shown to fail without it. " +
						"Write a test (in a _test file) that asserts the behaviour the request describes, including the edge cases it names, and run it; " +
						"pu reruns it against your pre-edit code automatically. Then finish."}},
				})
				continue
			}
			return finish(StatusCompleted, finalText, ""), nil
		}

		// Execute independent read/search calls in parallel via errgroup; serialize mutating tools.
		outcomes := make([]toolExecOutcome, len(toolCalls))
		results := make([]provider.ToolResultPart, len(toolCalls))
		eg, egCtx := errgroup.WithContext(runCtx)
		taintedSnapshot := untrustedTainted

		for i, tc := range toolCalls {
			i, tc := i, tc
			eg.Go(func() error {
				var oc toolExecOutcome
				if tc.Name == "patch" || tc.Name == "exec" || tc.Name == "plan" {
					mutMu.Lock()
					oc = executeToolCall(egCtx, cfg.RepoRoot, tracker, checkpointer, planMgr, taintedSnapshot, cfg.TrustUntrusted, cfg.Ablate.NoHashline, tc)
					mutMu.Unlock()
				} else {
					oc = executeToolCall(egCtx, cfg.RepoRoot, tracker, checkpointer, planMgr, taintedSnapshot, cfg.TrustUntrusted, cfg.Ablate.NoHashline, tc)
				}
				outcomes[i] = oc

				spilledContent, spilledPath, sErr := SpillOutput(cfg.RepoRoot, cfg.SessionID, tc.ID, oc.output)
				isErr := oc.isError
				if sErr != nil {
					spilledContent = fmt.Sprintf("error: spill output failed: %v", sErr)
					isErr = true
				}
				results[i] = provider.ToolResultPart{
					ToolCallID: tc.ID,
					Name:       tc.Name,
					Content:    spilledContent,
					IsError:    isErr,
				}
				logEvent("tool_result", map[string]any{
					"turn":         turns,
					"tool_call_id": tc.ID,
					"name":         tc.Name,
					"is_error":     isErr,
					"spilled_path": spilledPath,
					"bytes":        len(oc.output),
					"raw_output":   oc.output,
				})
				return nil
			})
		}
		_ = eg.Wait()

		blockedByGate := false
		for i, oc := range outcomes {
			toolMeta[toolCalls[i].ID] = oc.meta
			if oc.readOutside {
				untrustedTainted = true
			}
			if oc.blockedByGate {
				blockedByGate = true
			}
		}

		if blockedByGate {
			return finish(StatusBlocked, "", "exec blocked: untrusted external content in context (pass --trust-untrusted to override)"), nil
		}

		resultParts := make([]provider.ContentPart, len(results))
		for i := range results {
			resultParts[i] = results[i]
		}
		history = append(history, provider.Item{
			Role:  provider.RoleTool,
			Parts: resultParts,
		})
	}
}
