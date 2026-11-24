package core

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ddawnlll/pu/internal/catalog"
	"github.com/ddawnlll/pu/internal/edit"
	"github.com/ddawnlll/pu/internal/provider"
	"github.com/ddawnlll/pu/internal/store"
)

//go:embed system.txt
var SystemPrompt string

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

// Config configures one headless agent run.
type Config struct {
	RepoRoot      string
	SessionID     string
	Model         catalog.Model
	MaxCostUSD    float64
	MaxTurns      int
	Timeout       time.Duration
	Journal       *store.Journal
	OnStreamEvent func(provider.StreamEvent)
}

// Report is the machine-readable summary emitted at the end of a run.
type Report struct {
	Status          Status         `json:"status"`
	SessionID       string         `json:"session_id"`
	Model           string         `json:"model"`
	Turns           int            `json:"turns"`
	Tokens          provider.Usage `json:"tokens"`
	NotionalCostUSD float64        `json:"notional_cost_usd"`
	WallTimeMS      int64          `json:"wall_time_ms"`
	FinalText       string         `json:"final_text,omitempty"`
	Reason          string         `json:"reason,omitempty"`
}

// Run executes the single-writer agent loop with a 4-layer KV-invariant prefix,
// parallel independent tool execution via errgroup, 64 KiB output spill, and
// strict budget enforcement.
func Run(ctx context.Context, cfg Config, streamer Streamer, userPrompt string) (*Report, error) {
	startTime := time.Now()

	if cfg.RepoRoot == "" {
		cfg.RepoRoot = "."
	}
	if cfg.SessionID == "" {
		cfg.SessionID = fmt.Sprintf("pu-%d", startTime.UnixNano())
	}

	var runCtx context.Context
	var cancel context.CancelFunc
	if cfg.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
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
	layer1 := provider.Item{
		Role:  provider.RoleSystem,
		Parts: []provider.ContentPart{provider.TextPart{Text: strings.TrimSpace(SystemPrompt)}},
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

	// Turn-0 fingerprint goes into the first user turn (Layer 4), never system prompt.
	fingerprint := BuildTurn0Fingerprint(runCtx, cfg.RepoRoot)
	firstUserText := fingerprint + "\n\n" + userPrompt

	history := make([]provider.Item, 0, 16)
	history = append(history, prefixItems...)
	history = append(history, provider.Item{
		Role:  provider.RoleUser,
		Parts: []provider.ContentPart{provider.TextPart{Text: firstUserText}},
	})

	tracker := edit.NewTracker(cfg.RepoRoot)

	logEvent("session_start", map[string]any{
		"session_id": cfg.SessionID,
		"model":      cfg.Model.ID,
		"repo_root":  cfg.RepoRoot,
	})

	var totalUsage provider.Usage
	var totalCost float64
	turns := 0

	finish := func(status Status, finalText, reason string) *Report {
		rep := &Report{
			Status:          status,
			SessionID:       cfg.SessionID,
			Model:           cfg.Model.ID,
			Turns:           turns,
			Tokens:          totalUsage,
			NotionalCostUSD: totalCost,
			WallTimeMS:      time.Since(startTime).Milliseconds(),
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

		turns++
		req := provider.Request{
			Model:           cfg.Model.ID,
			SessionID:       cfg.SessionID,
			Turn:            turns,
			Items:           history,
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
			return finish(StatusCompleted, strings.TrimSpace(textBuf.String()), ""), nil
		}

		// Execute independent tool calls in parallel via errgroup, preserving order.
		results := make([]provider.ToolResultPart, len(toolCalls))
		eg, egCtx := errgroup.WithContext(runCtx)
		for i, tc := range toolCalls {
			i, tc := i, tc
			eg.Go(func() error {
				out, isErr := executeToolCall(egCtx, cfg.RepoRoot, tracker, tc)
				spilledContent, spilledPath, sErr := SpillOutput(cfg.RepoRoot, cfg.SessionID, tc.ID, out)
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
					"bytes":        len(out),
				})
				return nil
			})
		}
		_ = eg.Wait()

		// Return all tool results from this turn in one tool-result Item.
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
