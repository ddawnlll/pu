package eval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ddawnlll/pu/internal/catalog"
)

// BudgetSpec records the budget limits configured and actually enforced for a run.
type BudgetSpec struct {
	MaxCostUSD       float64 `json:"max_cost_usd,omitempty"`
	MaxTurns         int     `json:"max_turns,omitempty"`
	TimeoutMS        int64   `json:"timeout_ms,omitempty"`
	HarnessTimeoutMS int64   `json:"harness_timeout_ms,omitempty"`
	CostCapEnforced  bool    `json:"cost_cap_enforced"`
	TurnCapEnforced  bool    `json:"turn_cap_enforced"`
	TimeoutEnforced  bool    `json:"timeout_enforced"`
}

// Result is one line appended to results.jsonl.
type Result struct {
	Timestamp          time.Time  `json:"ts"`
	TaskID             string     `json:"task_id"`
	Category           string     `json:"category"`
	Tool               string     `json:"tool"` // "pu" or "jcode"
	OrderIndex         int        `json:"order_index"`
	Rep                int        `json:"rep,omitempty"`
	Model              string     `json:"model"`
	ReasoningEffort    string     `json:"reasoning_effort,omitempty"`
	ReasoningEnforced  bool       `json:"reasoning_enforced"`
	ReasoningNote      string     `json:"reasoning_note,omitempty"`
	Budget             BudgetSpec `json:"budget"`
	Passed             bool       `json:"passed"`
	TimedOut           bool       `json:"timed_out,omitempty"`
	RateLimited        bool       `json:"rate_limited,omitempty"`
	AgentStatus        string     `json:"agent_status"`
	Turns              int        `json:"turns"`
	WallTimeMS         int64      `json:"wall_time_ms"`                    // external process start -> exit
	InternalWallTimeMS int64      `json:"internal_wall_time_ms,omitempty"` // tool's self-reported wall time (e.g. pu)
	InputTokens        int        `json:"input_tokens"`
	CachedInputTokens  int        `json:"cached_input_tokens"`
	OutputTokens       int        `json:"output_tokens"`
	NotionalCostUSD    float64    `json:"notional_cost_usd"`
	AcceptanceExitCode int        `json:"acceptance_exit_code"`
	Error              string     `json:"error,omitempty"`
}

// AgentOutcome is returned by a single tool execution adapter (pu or jcode).
type AgentOutcome struct {
	Status             string
	Turns              int
	WallTimeMS         int64 // external process start -> exit
	InternalWallTimeMS int64 // tool self-reported wall time, if any
	InputTokens        int
	CachedInputTokens  int
	OutputTokens       int
	NotionalCostUSD    float64
	TimedOut           bool
	RateLimited        bool
	ReasoningEnforced  bool
	ReasoningNote      string
	Budget             BudgetSpec
	Err                error
}

// AgentRunner executes a tool on a prepared worktree directory.
type AgentRunner func(ctx context.Context, tool, worktreeDir string, task Task, cfg RunConfig) AgentOutcome

// RunConfig configures an evaluation run.
type RunConfig struct {
	TasksDir            string
	ResultsPath         string
	Model               string
	ModelsFile          string
	ReasoningEffort     string
	MaxCostUSD          float64
	MaxTurns            int
	Timeout             time.Duration // tool timeout
	HarnessTimeoutExtra time.Duration // extra grace added on top of Timeout (default 30s)
	SigintGrace         time.Duration // wait between SIGINT and SIGKILL on timeout (default 10s)
	MaxParallelTasks    int           // max tasks run concurrently (default 2; each task runs tools concurrently)
	PuBin               string
	JcodeBin            string
	JcodeTemplateHome   string   // optional pre-seeded JCODE_HOME template copied per run
	Tools               []string // e.g. ["pu", "jcode"] or ["pu"]
	Ablate              string
	Rep                 int         // repetition index recorded on results; resume keys include it
	SessionsDir         string      // if set, copy pu's .pu/sessions here per run before cleanup
	Resume              bool        // skip (task_id, tool) already completed (non-rate_limited) in ResultsPath
	Runner              AgentRunner // optional override for tests
}

// ToolOrder returns the spec §12 alternating execution order for a 1-based task
// index: odd tasks run jcode -> pu, even tasks run pu -> jcode.
func ToolOrder(taskNum1Based int, enabled []string) []string {
	has := make(map[string]bool, len(enabled))
	for _, t := range enabled {
		has[t] = true
	}
	var canonical []string
	if taskNum1Based%2 == 1 {
		canonical = []string{"jcode", "pu"}
	} else {
		canonical = []string{"pu", "jcode"}
	}
	out := make([]string, 0, len(canonical))
	for _, t := range canonical {
		if has[t] {
			out = append(out, t)
		}
	}
	return out
}

// IsRateLimited reports whether an error/output string indicates a provider
// HTTP 429 or OpenCode Go usage/quota limit.
func IsRateLimited(s string) bool {
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	needles := []string{
		"http 429",
		"status 429",
		"429 too many requests",
		"too many requests",
		"rate_limited",
		"rate-limited",
		"rate limit",
		"usage limit",
		"quota exceeded",
		"entitlementerror",
		"5-hour limit",
		"weekly limit",
		"monthly limit",
	}
	for _, n := range needles {
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
}

// RunAll runs all tasks in cfg.TasksDir in alternating order on fresh git
// worktrees, runs each task's independent acceptance command, and appends
// metrics to cfg.ResultsPath. When cfg.Resume is true, (task_id, tool) pairs
// that already have a non-rate_limited entry in cfg.ResultsPath are skipped.
func RunAll(ctx context.Context, cfg RunConfig) ([]Result, error) {
	tasks, err := LoadTasks(cfg.TasksDir)
	if err != nil {
		return nil, err
	}
	if len(cfg.Tools) == 0 {
		cfg.Tools = []string{"pu"}
		if cfg.JcodeBin != "" {
			if _, err := osexec.LookPath(cfg.JcodeBin); err == nil {
				cfg.Tools = []string{"pu", "jcode"}
			}
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}
	if cfg.HarnessTimeoutExtra <= 0 {
		cfg.HarnessTimeoutExtra = 30 * time.Second
	}
	if cfg.SigintGrace <= 0 {
		cfg.SigintGrace = 10 * time.Second
	}
	if cfg.MaxParallelTasks <= 0 {
		cfg.MaxParallelTasks = 2
	}
	if cfg.ReasoningEffort == "" {
		cfg.ReasoningEffort = "provider_default"
	}
	if cfg.Runner == nil {
		cfg.Runner = defaultAgentRunner
	}

	// Pre-seed one template JCODE_HOME (config + model/pricing caches + migration markers,
	// memory disabled, zero session history) and copy it per jcode run to eliminate
	// first-run cache fetches while keeping every run isolated.
	if cfg.JcodeTemplateHome == "" {
		for _, t := range cfg.Tools {
			if t == "jcode" {
				if tpl, tErr := prepareJcodeTemplateHome(cfg); tErr == nil {
					cfg.JcodeTemplateHome = tpl
					defer os.RemoveAll(tpl)
				}
				break
			}
		}
	}

	completedKeys := make(map[string]bool)
	if cfg.Resume {
		if existing, rErr := ReadResults(cfg.ResultsPath); rErr == nil {
			for _, r := range existing {
				if r.RateLimited || r.AgentStatus == "rate_limited" {
					continue
				}
				completedKeys[fmt.Sprintf("%s\x00%s\x00%d", r.TaskID, r.Tool, r.Rep)] = true
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(cfg.ResultsPath), 0o755); err != nil {
		return nil, fmt.Errorf("create results dir: %w", err)
	}
	resFile, err := os.OpenFile(cfg.ResultsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open results.jsonl: %w", err)
	}
	defer resFile.Close()

	var (
		mu       sync.Mutex
		all      []Result
		quotaHit atomic.Bool
		taskWG   sync.WaitGroup
	)
	sem := make(chan struct{}, cfg.MaxParallelTasks)

	for idx, task := range tasks {
		if quotaHit.Load() || ctx.Err() != nil {
			break
		}
		order := ToolOrder(idx+1, cfg.Tools)
		pendingTools := make([]struct {
			tool     string
			orderIdx int
		}, 0, len(order))
		for orderPos, tool := range order {
			if cfg.Resume && completedKeys[fmt.Sprintf("%s\x00%s\x00%d", task.ID, tool, cfg.Rep)] {
				continue
			}
			pendingTools = append(pendingTools, struct {
				tool     string
				orderIdx int
			}{tool: tool, orderIdx: orderPos + 1})
		}
		if len(pendingTools) == 0 {
			continue
		}

		sem <- struct{}{}
		taskWG.Add(1)
		go func(t Task, toolsToRun []struct {
			tool     string
			orderIdx int
		}) {
			defer taskWG.Done()
			defer func() { <-sem }()

			// Run pu and jcode concurrently in separate worktrees for the same task.
			var toolWG sync.WaitGroup
			for _, item := range toolsToRun {
				if quotaHit.Load() || ctx.Err() != nil {
					continue
				}
				toolWG.Add(1)
				go func(tool string, orderIdx int) {
					defer toolWG.Done()
					res := runOne(ctx, tool, orderIdx, t, cfg)
					if res.RateLimited {
						quotaHit.Store(true)
					}
					mu.Lock()
					all = append(all, res)
					if line, mErr := json.Marshal(res); mErr == nil {
						_, _ = resFile.Write(append(line, '\n'))
					}
					mu.Unlock()
				}(item.tool, item.orderIdx)
			}
			toolWG.Wait()
		}(task, pendingTools)
	}
	taskWG.Wait()
	return all, nil
}

func runOne(ctx context.Context, tool string, orderIdx int, task Task, cfg RunConfig) Result {
	budget := defaultBudgetForTool(tool, cfg)
	wtDir, cleanup, err := createWorktree(task.Repo, task.Commit)
	if err != nil {
		return Result{
			Timestamp:          time.Now().UTC(),
			TaskID:             task.ID,
			Category:           task.Category,
			Tool:               tool,
			OrderIndex:         orderIdx,
			Rep:                cfg.Rep,
			Model:              cfg.Model,
			ReasoningEffort:    cfg.ReasoningEffort,
			Budget:             budget,
			AgentStatus:        "setup_error",
			AcceptanceExitCode: -1,
			Error:              err.Error(),
		}
	}
	defer cleanup()
	if cfg.SessionsDir != "" && tool == "pu" {
		// Preserve pu's session journal (turn-level events) before the worktree is removed.
		defer func() {
			dst := filepath.Join(cfg.SessionsDir, fmt.Sprintf("%s__rep%d", task.ID, cfg.Rep))
			_ = os.MkdirAll(dst, 0o755)
			_ = osexec.Command("cp", "-r", filepath.Join(wtDir, ".pu", "sessions"), dst).Run()
		}()
	}

	harnessTimeout := cfg.Timeout + cfg.HarnessTimeoutExtra
	runCtx, cancel := context.WithTimeout(ctx, harnessTimeout+cfg.SigintGrace+5*time.Second)
	defer cancel()

	// Measure external wall time consistently from process start to exit for both tools.
	extStart := time.Now()
	outcome := cfg.Runner(runCtx, tool, wtDir, task, cfg)
	extWallMS := time.Since(extStart).Milliseconds()
	if outcome.WallTimeMS > 0 {
		// Allow adapters (which wrap cmd start->wait directly) or test fakes to provide exact process wall time.
		extWallMS = outcome.WallTimeMS
	}

	if outcome.Budget != (BudgetSpec{}) {
		budget = outcome.Budget
	}

	errStr := ""
	if outcome.Err != nil {
		errStr = outcome.Err.Error()
	}
	rateLimited := outcome.RateLimited || outcome.Status == "rate_limited" || IsRateLimited(errStr)
	agentStatus := outcome.Status
	if rateLimited {
		agentStatus = "rate_limited"
	}

	// A run counts as successful iff the acceptance test passes, whatever status
	// the agent reported (excluding rate_limited and setup_error).
	accExit := -1
	passed := false
	if !rateLimited && agentStatus != "setup_error" {
		fetchForAcceptance(task.Repo, wtDir)
		accExit = runAcceptance(ctx, wtDir, task.Acceptance)
		passed = accExit == 0
	}

	return Result{
		Timestamp:          time.Now().UTC(),
		TaskID:             task.ID,
		Category:           task.Category,
		Tool:               tool,
		OrderIndex:         orderIdx,
		Rep:                cfg.Rep,
		Model:              cfg.Model,
		ReasoningEffort:    cfg.ReasoningEffort,
		ReasoningEnforced:  outcome.ReasoningEnforced,
		ReasoningNote:      outcome.ReasoningNote,
		Budget:             budget,
		Passed:             passed,
		TimedOut:           outcome.TimedOut,
		RateLimited:        rateLimited,
		AgentStatus:        agentStatus,
		Turns:              outcome.Turns,
		WallTimeMS:         extWallMS,
		InternalWallTimeMS: outcome.InternalWallTimeMS,
		InputTokens:        outcome.InputTokens,
		CachedInputTokens:  outcome.CachedInputTokens,
		OutputTokens:       outcome.OutputTokens,
		NotionalCostUSD:    outcome.NotionalCostUSD,
		AcceptanceExitCode: accExit,
		Error:              errStr,
	}
}

func defaultBudgetForTool(tool string, cfg RunConfig) BudgetSpec {
	harnessExtra := cfg.HarnessTimeoutExtra
	if harnessExtra <= 0 {
		harnessExtra = 30 * time.Second
	}
	b := BudgetSpec{
		TimeoutMS:        cfg.Timeout.Milliseconds(),
		HarnessTimeoutMS: (cfg.Timeout + harnessExtra).Milliseconds(),
		TimeoutEnforced:  cfg.Timeout > 0,
	}
	if tool == "pu" {
		b.MaxCostUSD = cfg.MaxCostUSD
		b.MaxTurns = cfg.MaxTurns
		b.CostCapEnforced = cfg.MaxCostUSD > 0
		b.TurnCapEnforced = cfg.MaxTurns > 0
	}
	return b
}

// createWorktree builds an isolated checkout of commit that contains only the
// commit's own history: no branches, tags, remotes or future commits, so the
// agent can't read the upstream fix via git log/show. Future objects are
// fetched in only for acceptance by fetchForAcceptance. Falls back to a plain
// worktree if the isolated fetch fails (e.g. commit unreachable by fetch).
func createWorktree(repoDir, commit string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "pu-eval-wt-*")
	if err != nil {
		return "", nil, fmt.Errorf("mkdirtemp worktree: %w", err)
	}
	if isoErr := isolatedCheckout(repoDir, commit, tmpDir); isoErr == nil {
		return tmpDir, func() { _ = os.RemoveAll(tmpDir) }, nil
	}
	_ = os.RemoveAll(tmpDir)
	_ = os.MkdirAll(tmpDir, 0o755)
	cmd := osexec.Command("git", "worktree", "add", "--detach", tmpDir, commit)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", nil, fmt.Errorf("git worktree add (%s @ %s): %w (%s)", repoDir, commit, err, strings.TrimSpace(string(out)))
	}
	cleanup := func() {
		rm := osexec.Command("git", "worktree", "remove", "--force", tmpDir)
		rm.Dir = repoDir
		_ = rm.Run()
		_ = os.RemoveAll(tmpDir)
	}
	return tmpDir, cleanup, nil
}

func runAcceptance(ctx context.Context, worktreeDir, acceptanceCmd string) int {
	accCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := osexec.CommandContext(accCtx, "/bin/sh", "-c", acceptanceCmd)
	cmd.Dir = worktreeDir
	if err := cmd.Run(); err != nil {
		var exitErr *osexec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return -1
	}
	return 0
}

func defaultAgentRunner(ctx context.Context, tool, worktreeDir string, task Task, cfg RunConfig) AgentOutcome {
	switch tool {
	case "pu":
		return runPu(ctx, worktreeDir, task, cfg)
	case "jcode":
		return runJcode(ctx, worktreeDir, task, cfg)
	default:
		return AgentOutcome{Status: "failed", Err: fmt.Errorf("unknown eval tool %q", tool)}
	}
}

func runPu(ctx context.Context, worktreeDir string, task Task, cfg RunConfig) AgentOutcome {
	puBin := cfg.PuBin
	if puBin == "" {
		puBin = "pu"
	}
	args := []string{"run", "--headless"}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.ModelsFile != "" {
		args = append(args, "--models", cfg.ModelsFile)
	}
	if cfg.ReasoningEffort != "" {
		args = append(args, "--reasoning-effort", cfg.ReasoningEffort)
	}
	if cfg.MaxCostUSD > 0 {
		args = append(args, "--max-cost-usd", fmt.Sprintf("%.4f", cfg.MaxCostUSD))
	}
	if cfg.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", cfg.MaxTurns))
	}
	if cfg.Timeout > 0 {
		args = append(args, "--timeout", cfg.Timeout.String())
	}
	if cfg.Ablate != "" {
		args = append(args, "--ablate", cfg.Ablate)
	}
	args = append(args, task.Prompt)

	cmd := osexec.Command(puBin, args...)
	cmd.Dir = worktreeDir
	if devNull, nErr := os.Open(os.DevNull); nErr == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	harnessTimeout, sigintGrace := effectiveTimeouts(cfg)

	// External wall time: process start to exit.
	start := time.Now()
	harnessTimedOut, err := runCmdWithGracefulInterrupt(ctx, cmd, harnessTimeout, sigintGrace)
	extElapsed := time.Since(start).Milliseconds()

	combinedErrText := stderr.String() + "\n" + stdout.String()
	budget := defaultBudgetForTool("pu", cfg)

	var rep struct {
		Status          string  `json:"status"`
		Reason          string  `json:"reason"`
		Turns           int     `json:"turns"`
		WallTimeMS      int64   `json:"wall_time_ms"`
		NotionalCostUSD float64 `json:"notional_cost_usd"`
		Tokens          struct {
			InputTokens       int `json:"input_tokens"`
			CachedInputTokens int `json:"cached_input_tokens"`
			OutputTokens      int `json:"output_tokens"`
		} `json:"tokens"`
	}
	if jErr := json.Unmarshal(stdout.Bytes(), &rep); jErr == nil && rep.Status != "" {
		rateLimited := IsRateLimited(rep.Reason) || IsRateLimited(stderr.String())
		timedOut := harnessTimedOut || strings.Contains(strings.ToLower(rep.Reason), "timeout")
		status := rep.Status
		if rateLimited {
			status = "rate_limited"
		}
		return AgentOutcome{
			Status:             status,
			Turns:              rep.Turns,
			WallTimeMS:         extElapsed,     // external process wall time
			InternalWallTimeMS: rep.WallTimeMS, // pu self-reported wall time kept separate
			InputTokens:        rep.Tokens.InputTokens,
			CachedInputTokens:  rep.Tokens.CachedInputTokens,
			OutputTokens:       rep.Tokens.OutputTokens,
			NotionalCostUSD:    rep.NotionalCostUSD,
			TimedOut:           timedOut,
			RateLimited:        rateLimited,
			ReasoningEnforced:  cfg.ReasoningEffort != "",
			Budget:             budget,
			Err:                err,
		}
	}

	rateLimited := IsRateLimited(combinedErrText)
	status := "failed"
	if err == nil {
		status = "completed"
	}
	if rateLimited {
		status = "rate_limited"
	}
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return AgentOutcome{
		Status:            status,
		WallTimeMS:        extElapsed,
		TimedOut:          harnessTimedOut,
		RateLimited:       rateLimited,
		ReasoningEnforced: cfg.ReasoningEffort != "",
		Budget:            budget,
		Err:               err,
	}
}

// runJcode is the jcode 1.0 adapter built from `jcode run --help`.
// Each invocation runs with an isolated, fresh JCODE_HOME + XDG config/data/cache
// directory so no memory, sessions, or state carry between tasks.
func runJcode(ctx context.Context, worktreeDir string, task Task, cfg RunConfig) AgentOutcome {
	jcodeBin := cfg.JcodeBin
	if jcodeBin == "" {
		jcodeBin = "jcode"
	}

	budget := defaultBudgetForTool("jcode", cfg)
	isoHome, err := os.MkdirTemp("", "pu-eval-jcode-home-*")
	if err != nil {
		return AgentOutcome{Status: "setup_error", Budget: budget, Err: fmt.Errorf("create isolated jcode home: %w", err)}
	}
	defer os.RemoveAll(isoHome)

	isProviderDefault := cfg.ReasoningEffort == "" || strings.EqualFold(cfg.ReasoningEffort, "provider_default") || strings.EqualFold(cfg.ReasoningEffort, "default") || strings.EqualFold(cfg.ReasoningEffort, "none")
	if cfg.JcodeTemplateHome != "" {
		_ = copyDir(cfg.JcodeTemplateHome, isoHome)
	} else {
		_ = seedJcodeHome(isoHome, cfg)
	}

	args := []string{
		"run",
		"--json",
		"-p", "opencode-go",
		"-C", worktreeDir,
		"--no-update",
		"--no-selfdev",
		"--quiet",
	}
	if cfg.Model != "" {
		args = append(args, "-m", cfg.Model)
	}
	args = append(args, task.Prompt)

	cmd := osexec.Command(jcodeBin, args...)
	cmd.Dir = worktreeDir
	cmd.Env = isolatedJcodeEnv(os.Environ(), isoHome)
	if devNull, nErr := os.Open(os.DevNull); nErr == nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	harnessTimeout, sigintGrace := effectiveTimeouts(cfg)

	// External wall time: process start to exit.
	start := time.Now()
	harnessTimedOut, runErr := runCmdWithGracefulInterrupt(ctx, cmd, harnessTimeout, sigintGrace)
	extElapsed := time.Since(start).Milliseconds()

	combinedErr := stderr.String() + "\n" + stdout.String()
	rateLimited := IsRateLimited(combinedErr)

	reasoningEnforced := isProviderDefault
	reasoningNote := "both tools use provider default (verified jcode opencode-go ignores openai_reasoning_effort and sends thinking_enabled=None)"
	if !isProviderDefault {
		reasoningNote = "jcode opencode-go ignores openai_reasoning_effort and sends provider default (thinking_enabled=None)"
	}
	out := AgentOutcome{
		Status:            "completed",
		WallTimeMS:        extElapsed,
		TimedOut:          harnessTimedOut,
		RateLimited:       rateLimited,
		ReasoningEnforced: reasoningEnforced,
		ReasoningNote:     reasoningNote,
		Budget:            budget,
		Err:               runErr,
	}
	if runErr != nil {
		out.Status = "failed"
		if stderr.Len() > 0 {
			out.Err = fmt.Errorf("%w: %s", runErr, strings.TrimSpace(stderr.String()))
		}
	}
	if rateLimited {
		out.Status = "rate_limited"
	}

	// Primary usage source: sum ALL turns from isolated JCODE_HOME session journals/logs
	// because `jcode run --json` only emits the last turn's usage.
	sessUsage := ParseJcodeSessionUsage(isoHome)
	if sessUsage.Turns > 0 {
		out.Turns = sessUsage.Turns
		out.InputTokens = sessUsage.InputTokens
		out.CachedInputTokens = sessUsage.CachedInputTokens
		out.OutputTokens = sessUsage.OutputTokens
	} else {
		// Fallback to jcode --json stdout when no session logs exist.
		var jResp map[string]any
		if json.Unmarshal(stdout.Bytes(), &jResp) == nil {
			if u, ok := jResp["usage"].(map[string]any); ok {
				out.InputTokens = intFromAny(u["input_tokens"])
				out.CachedInputTokens = intFromAny(u["cached_input_tokens"])
				if out.CachedInputTokens == 0 {
					out.CachedInputTokens = intFromAny(u["cache_read_input_tokens"])
				}
				out.OutputTokens = intFromAny(u["output_tokens"])
			}
			if c, ok := jResp["cost_usd"].(float64); ok {
				out.NotionalCostUSD = c
			}
			if t := intFromAny(jResp["turns"]); t > 0 {
				out.Turns = t
			}
		}
	}
	// Always compute notional list-price cost from models.toml on cumulative tokens
	// for apples-to-apples parity with pu.
	if (out.InputTokens > 0 || out.OutputTokens > 0) && cfg.ModelsFile != "" {
		if cat, cErr := catalog.Load(cfg.ModelsFile); cErr == nil {
			if m, mErr := cat.Model(cfg.Model); mErr == nil {
				out.NotionalCostUSD = m.ComputeCostUSD(out.InputTokens, out.CachedInputTokens, out.OutputTokens)
			}
		}
	}
	return out
}

// JcodeUsageSummary holds cumulative token usage summed across all turns
// recorded in an isolated JCODE_HOME directory.
type JcodeUsageSummary struct {
	Turns             int
	InputTokens       int
	CachedInputTokens int
	OutputTokens      int
}

type jcodeTokenUsage struct {
	PromptTokens         int `json:"prompt_tokens"`
	InputTokens          int `json:"input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	CacheReadInputTokens int `json:"cache_read_input_tokens"`
	CachedInputTokens    int `json:"cached_input_tokens"`
}

type jcodeSessionMsg struct {
	ID         string           `json:"id"`
	Role       string           `json:"role"`
	TokenUsage *jcodeTokenUsage `json:"token_usage"`
}

var jcodeAPICallLogRE = regexp.MustCompile(`API call complete in [0-9.]+s \(input=(\d+) output=(\d+) cache_read=(\d+)`)

// ParseJcodeSessionUsage sums per-turn token usage across all session files in
// an isolated JCODE_HOME directory (sessions/*.journal.jsonl and sessions/*.json,
// deduplicated by message ID, with a fallback to logs/jcode-*.log).
func ParseJcodeSessionUsage(jcodeHome string) JcodeUsageSummary {
	var sum JcodeUsageSummary
	seenMsg := make(map[string]bool)

	addMsg := func(m jcodeSessionMsg) {
		if m.TokenUsage == nil {
			return
		}
		if m.Role != "" && m.Role != "assistant" {
			return
		}
		if m.ID != "" {
			if seenMsg[m.ID] {
				return
			}
			seenMsg[m.ID] = true
		}
		inTok := m.TokenUsage.InputTokens
		if inTok == 0 {
			inTok = m.TokenUsage.PromptTokens
		}
		cachedTok := m.TokenUsage.CacheReadInputTokens
		if cachedTok == 0 {
			cachedTok = m.TokenUsage.CachedInputTokens
		}
		if inTok == 0 && m.TokenUsage.OutputTokens == 0 {
			return
		}
		sum.Turns++
		sum.InputTokens += inTok
		sum.CachedInputTokens += cachedTok
		sum.OutputTokens += m.TokenUsage.OutputTokens
	}

	// 1. Read sessions/*.journal.jsonl (append_messages per turn).
	journals, _ := filepath.Glob(filepath.Join(jcodeHome, "sessions", "*.journal.jsonl"))
	for _, jp := range journals {
		f, err := os.Open(jp)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var entry struct {
				AppendMessages []jcodeSessionMsg `json:"append_messages"`
			}
			if json.Unmarshal([]byte(line), &entry) == nil {
				for _, m := range entry.AppendMessages {
					addMsg(m)
				}
			}
		}
		_ = f.Close()
	}

	// 2. Also scan sessions/*.json (initial snapshot messages, deduplicated by id).
	snaps, _ := filepath.Glob(filepath.Join(jcodeHome, "sessions", "*.json"))
	for _, sp := range snaps {
		data, err := os.ReadFile(sp)
		if err != nil {
			continue
		}
		var sess struct {
			Messages []jcodeSessionMsg `json:"messages"`
		}
		if json.Unmarshal(data, &sess) == nil {
			for _, m := range sess.Messages {
				addMsg(m)
			}
		}
	}

	if sum.Turns > 0 {
		return sum
	}

	// 3. Fallback: parse logs/jcode-*.log "API call complete ... (input=... output=... cache_read=...)".
	logs, _ := filepath.Glob(filepath.Join(jcodeHome, "logs", "jcode-*.log"))
	for _, lp := range logs {
		f, err := os.Open(lp)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 2*1024*1024)
		for sc.Scan() {
			m := jcodeAPICallLogRE.FindStringSubmatch(sc.Text())
			if len(m) == 4 {
				inTok, _ := strconv.Atoi(m[1])
				outTok, _ := strconv.Atoi(m[2])
				cacheTok, _ := strconv.Atoi(m[3])
				sum.Turns++
				sum.InputTokens += inTok
				sum.OutputTokens += outTok
				sum.CachedInputTokens += cacheTok
			}
		}
		_ = f.Close()
	}

	return sum
}

func prepareJcodeTemplateHome(cfg RunConfig) (string, error) {
	tpl, err := os.MkdirTemp("", "pu-eval-jcode-template-*")
	if err != nil {
		return "", err
	}
	if err := seedJcodeHome(tpl, cfg); err != nil {
		_ = os.RemoveAll(tpl)
		return "", err
	}
	return tpl, nil
}

// seedJcodeHome writes config.toml (memory disabled) and copies read-only
// model/pricing caches and migration stamps (never sessions, state, or memory DBs)
// so fresh runs avoid first-run network fetches.
func seedJcodeHome(dst string, cfg RunConfig) error {
	var conf strings.Builder
	conf.WriteString("[agents]\nmemory_sidecar_enabled = false\n")
	isProviderDefault := cfg.ReasoningEffort == "" || strings.EqualFold(cfg.ReasoningEffort, "provider_default") || strings.EqualFold(cfg.ReasoningEffort, "default") || strings.EqualFold(cfg.ReasoningEffort, "none")
	if !isProviderDefault {
		fmt.Fprintf(&conf, "\n[provider]\nopenai_reasoning_effort = %q\n", cfg.ReasoningEffort)
	}
	if err := os.WriteFile(filepath.Join(dst, "config.toml"), []byte(conf.String()), 0o644); err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Join(dst, "migrations"), 0o755)
	_ = os.WriteFile(filepath.Join(dst, "migrations", "idle-animation-off"), []byte("1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dst, "migrations", "swarm-spawn-mode-inline"), []byte("1\n"), 0o644)

	// Copy static model/pricing caches from existing ~/.jcode/cache if available
	// (strictly allowlisted cache files; never session search indexes or session DBs).
	if home, err := os.UserHomeDir(); err == nil {
		srcCache := filepath.Join(home, ".jcode", "cache")
		dstCache := filepath.Join(dst, "cache")
		allowCache := []string{"opencode-go_models.json", "models_dev_pricing.json"}
		for _, name := range allowCache {
			src := filepath.Join(srcCache, name)
			if st, sErr := os.Stat(src); sErr == nil && !st.IsDir() {
				_ = os.MkdirAll(dstCache, 0o755)
				_ = copyFile(src, filepath.Join(dstCache, name))
			}
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func isolatedJcodeEnv(baseEnv []string, isoHome string) []string {
	overrides := map[string]string{
		"JCODE_HOME":         isoHome,
		"XDG_CONFIG_HOME":    filepath.Join(isoHome, "config"),
		"XDG_DATA_HOME":      filepath.Join(isoHome, "data"),
		"XDG_CACHE_HOME":     filepath.Join(isoHome, "cache"),
		"JCODE_NO_TELEMETRY": "1",
	}
	var opencodeKey, opencodeGoKey string
	out := make([]string, 0, len(baseEnv)+len(overrides)+1)
	for _, kv := range baseEnv {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if k == "OPENCODE_API_KEY" {
			opencodeKey = v
		}
		if k == "OPENCODE_GO_API_KEY" {
			opencodeGoKey = v
		}
		if _, replaced := overrides[k]; replaced {
			continue
		}
		out = append(out, kv)
	}
	if opencodeGoKey == "" && opencodeKey != "" {
		overrides["OPENCODE_GO_API_KEY"] = opencodeKey
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

func effectiveTimeouts(cfg RunConfig) (harnessTimeout, sigintGrace time.Duration) {
	extra := cfg.HarnessTimeoutExtra
	if extra <= 0 {
		extra = 30 * time.Second
	}
	grace := cfg.SigintGrace
	if grace <= 0 {
		grace = 10 * time.Second
	}
	if cfg.Timeout > 0 {
		harnessTimeout = cfg.Timeout + extra
	}
	return harnessTimeout, grace
}

// runCmdWithGracefulInterrupt starts cmd in its own process group. If
// harnessTimeout expires (or ctx is canceled), it sends SIGINT to the process
// group, waits up to sigintGrace for a clean exit (so pu can flush its JSON
// report), and only then sends SIGKILL.
func runCmdWithGracefulInterrupt(ctx context.Context, cmd *osexec.Cmd, harnessTimeout, sigintGrace time.Duration) (bool, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var timerC <-chan time.Time
	var timer *time.Timer
	if harnessTimeout > 0 {
		timer = time.NewTimer(harnessTimeout)
		defer timer.Stop()
		timerC = timer.C
	}

	select {
	case err := <-done:
		return false, err
	case <-timerC:
		pgid := cmd.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGINT)
		select {
		case err := <-done:
			return true, err
		case <-time.After(sigintGrace):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			return true, <-done
		}
	case <-ctx.Done():
		pgid := cmd.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGINT)
		select {
		case err := <-done:
			return true, err
		case <-time.After(sigintGrace):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			return true, <-done
		}
	}
}

func intFromAny(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}
