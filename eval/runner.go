package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ddawnlll/pu/internal/catalog"
)

// Result is one line appended to results.jsonl.
type Result struct {
	Timestamp          time.Time `json:"ts"`
	TaskID             string    `json:"task_id"`
	Category           string    `json:"category"`
	Tool               string    `json:"tool"` // "pu" or "jcode"
	OrderIndex         int       `json:"order_index"`
	Model              string    `json:"model"`
	Passed             bool      `json:"passed"`
	AgentStatus        string    `json:"agent_status"`
	Turns              int       `json:"turns"`
	WallTimeMS         int64     `json:"wall_time_ms"`
	InputTokens        int       `json:"input_tokens"`
	CachedInputTokens  int       `json:"cached_input_tokens"`
	OutputTokens       int       `json:"output_tokens"`
	NotionalCostUSD    float64   `json:"notional_cost_usd"`
	AcceptanceExitCode int       `json:"acceptance_exit_code"`
	Error              string    `json:"error,omitempty"`
}

// AgentOutcome is returned by a single tool execution adapter (pu or jcode).
type AgentOutcome struct {
	Status            string
	Turns             int
	WallTimeMS        int64
	InputTokens       int
	CachedInputTokens int
	OutputTokens      int
	NotionalCostUSD   float64
	Err               error
}

// AgentRunner executes a tool on a prepared worktree directory.
type AgentRunner func(ctx context.Context, tool, worktreeDir string, task Task, cfg RunConfig) AgentOutcome

// RunConfig configures an evaluation run.
type RunConfig struct {
	TasksDir    string
	ResultsPath string
	Model       string
	ModelsFile  string
	MaxCostUSD  float64
	MaxTurns    int
	Timeout     time.Duration
	PuBin       string
	JcodeBin    string
	Tools       []string // e.g. ["pu", "jcode"] or ["pu"]
	Ablate      string
	Runner      AgentRunner // optional override for tests
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

// RunAll runs all tasks in cfg.TasksDir in alternating order on fresh git
// worktrees, runs each task's independent acceptance command, and appends
// metrics to cfg.ResultsPath.
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
	if cfg.Runner == nil {
		cfg.Runner = defaultAgentRunner
	}

	if err := os.MkdirAll(filepath.Dir(cfg.ResultsPath), 0o755); err != nil {
		return nil, fmt.Errorf("create results dir: %w", err)
	}
	resFile, err := os.OpenFile(cfg.ResultsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open results.jsonl: %w", err)
	}
	defer resFile.Close()

	var all []Result
	for idx, task := range tasks {
		order := ToolOrder(idx+1, cfg.Tools)
		for orderPos, tool := range order {
			res := runOne(ctx, tool, orderPos+1, task, cfg)
			all = append(all, res)

			line, mErr := json.Marshal(res)
			if mErr == nil {
				_, _ = resFile.Write(append(line, '\n'))
			}
		}
	}
	return all, nil
}

func runOne(ctx context.Context, tool string, orderIdx int, task Task, cfg RunConfig) Result {
	wtDir, cleanup, err := createWorktree(task.Repo, task.Commit)
	if err != nil {
		return Result{
			Timestamp:          time.Now().UTC(),
			TaskID:             task.ID,
			Category:           task.Category,
			Tool:               tool,
			OrderIndex:         orderIdx,
			Model:              cfg.Model,
			AgentStatus:        "setup_error",
			AcceptanceExitCode: -1,
			Error:              err.Error(),
		}
	}
	defer cleanup()

	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	start := time.Now()
	outcome := cfg.Runner(runCtx, tool, wtDir, task, cfg)
	if outcome.WallTimeMS == 0 {
		outcome.WallTimeMS = time.Since(start).Milliseconds()
	}

	accExit := runAcceptance(ctx, wtDir, task.Acceptance)

	errStr := ""
	if outcome.Err != nil {
		errStr = outcome.Err.Error()
	}
	return Result{
		Timestamp:          time.Now().UTC(),
		TaskID:             task.ID,
		Category:           task.Category,
		Tool:               tool,
		OrderIndex:         orderIdx,
		Model:              cfg.Model,
		Passed:             accExit == 0,
		AgentStatus:        outcome.Status,
		Turns:              outcome.Turns,
		WallTimeMS:         outcome.WallTimeMS,
		InputTokens:        outcome.InputTokens,
		CachedInputTokens:  outcome.CachedInputTokens,
		OutputTokens:       outcome.OutputTokens,
		NotionalCostUSD:    outcome.NotionalCostUSD,
		AcceptanceExitCode: accExit,
		Error:              errStr,
	}
}

func createWorktree(repoDir, commit string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "pu-eval-wt-*")
	if err != nil {
		return "", nil, fmt.Errorf("mkdirtemp worktree: %w", err)
	}
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

	cmd := osexec.CommandContext(ctx, puBin, args...)
	cmd.Dir = worktreeDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	var rep struct {
		Status          string  `json:"status"`
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
		wt := rep.WallTimeMS
		if wt == 0 {
			wt = elapsed
		}
		return AgentOutcome{
			Status:            rep.Status,
			Turns:             rep.Turns,
			WallTimeMS:        wt,
			InputTokens:       rep.Tokens.InputTokens,
			CachedInputTokens: rep.Tokens.CachedInputTokens,
			OutputTokens:      rep.Tokens.OutputTokens,
			NotionalCostUSD:   rep.NotionalCostUSD,
			Err:               err,
		}
	}

	status := "failed"
	if err == nil {
		status = "completed"
	}
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return AgentOutcome{
		Status:     status,
		WallTimeMS: elapsed,
		Err:        err,
	}
}

// runJcode is the jcode 1.0 adapter built from `jcode run --help`.
// Invokes: jcode run --json -p opencode-go -m <model> -C <worktree> --no-update --no-selfdev --quiet <prompt>
func runJcode(ctx context.Context, worktreeDir string, task Task, cfg RunConfig) AgentOutcome {
	jcodeBin := cfg.JcodeBin
	if jcodeBin == "" {
		jcodeBin = "jcode"
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

	cmd := osexec.CommandContext(ctx, jcodeBin, args...)
	cmd.Dir = worktreeDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	out := AgentOutcome{
		Status:     "completed",
		WallTimeMS: elapsed,
		Err:        err,
	}
	if err != nil {
		out.Status = "failed"
		if stderr.Len() > 0 {
			out.Err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
	}

	// Best-effort parse of jcode --json output for token/cost metrics if present.
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
	if out.NotionalCostUSD == 0 && (out.InputTokens > 0 || out.OutputTokens > 0) && cfg.ModelsFile != "" {
		if cat, cErr := catalog.Load(cfg.ModelsFile); cErr == nil {
			if m, mErr := cat.Model(cfg.Model); mErr == nil {
				out.NotionalCostUSD = m.ComputeCostUSD(out.InputTokens, out.CachedInputTokens, out.OutputTokens)
			}
		}
	}
	return out
}

func intFromAny(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}
