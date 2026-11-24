package eval

import (
	"context"
	"errors"
	"math"
	"os"
	osexec "os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ddawnlll/pu/internal/catalog"
)

func TestAlternatingToolOrder(t *testing.T) {
	both := []string{"pu", "jcode"}
	if got := ToolOrder(1, both); !reflect.DeepEqual(got, []string{"jcode", "pu"}) {
		t.Fatalf("odd task 1 order = %v, want [jcode pu]", got)
	}
	if got := ToolOrder(2, both); !reflect.DeepEqual(got, []string{"pu", "jcode"}) {
		t.Fatalf("even task 2 order = %v, want [pu jcode]", got)
	}
	if got := ToolOrder(3, both); !reflect.DeepEqual(got, []string{"jcode", "pu"}) {
		t.Fatalf("odd task 3 order = %v, want [jcode pu]", got)
	}
}

func TestSummarizeGoNoGoVerdict(t *testing.T) {
	// GO case: equal success rate (100%), pu 25% faster.
	goSummary := Summarize([]Result{
		{TaskID: "t1", Tool: "jcode", Passed: true, WallTimeMS: 10000, NotionalCostUSD: 0.02},
		{TaskID: "t1", Tool: "pu", Passed: true, WallTimeMS: 7500, NotionalCostUSD: 0.02},
	})
	if goSummary.Verdict != "GO" {
		t.Fatalf("expected GO, got %s (%s)", goSummary.Verdict, goSummary.VerdictMsg)
	}

	// NO-GO case: pu lower success rate even though faster.
	nogoRate := Summarize([]Result{
		{TaskID: "t1", Tool: "jcode", Passed: true, WallTimeMS: 10000, NotionalCostUSD: 0.02},
		{TaskID: "t2", Tool: "jcode", Passed: true, WallTimeMS: 10000, NotionalCostUSD: 0.02},
		{TaskID: "t1", Tool: "pu", Passed: true, WallTimeMS: 5000, NotionalCostUSD: 0.01},
		{TaskID: "t2", Tool: "pu", Passed: false, WallTimeMS: 5000, NotionalCostUSD: 0.01},
	})
	if nogoRate.Verdict != "NO-GO" {
		t.Fatalf("expected NO-GO on lower success rate, got %s", nogoRate.Verdict)
	}

	// NO-GO case: equal success rate, <15% gain on both time and cost.
	nogoGain := Summarize([]Result{
		{TaskID: "t1", Tool: "jcode", Passed: true, WallTimeMS: 10000, NotionalCostUSD: 0.020},
		{TaskID: "t1", Tool: "pu", Passed: true, WallTimeMS: 9200, NotionalCostUSD: 0.019},
	})
	if nogoGain.Verdict != "NO-GO" {
		t.Fatalf("expected NO-GO on <15%% gain, got %s", nogoGain.Verdict)
	}
}

func TestDryRunWorktreesAlternatingAndResultsJSONL(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "test")
	runGit(t, repo, "config", "user.email", "test@example.com")
	_ = os.WriteFile(filepath.Join(repo, "val.txt"), []byte("bad\n"), 0o644)
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "init")

	tasksDir := filepath.Join(tmp, "tasks")
	for _, id := range []string{"01_task", "02_task", "03_task"} {
		d := filepath.Join(tasksDir, id)
		_ = os.MkdirAll(d, 0o755)
		toml := "id = \"" + id + "\"\nrepo = \"" + filepath.ToSlash(repo) + "\"\ncommit = \"HEAD\"\ncategory = \"bugfix\"\nprompt = \"fix val.txt\"\nacceptance = \"grep -q good val.txt\"\n"
		_ = os.WriteFile(filepath.Join(d, "task.toml"), []byte(toml), 0o644)
	}

	var observedOrder []string
	resultsPath := filepath.Join(tmp, "results.jsonl")
	results, err := RunAll(context.Background(), RunConfig{
		TasksDir:    tasksDir,
		ResultsPath: resultsPath,
		Model:       "deepseek-v4.1-flash",
		Timeout:     30 * time.Second,
		Tools:       []string{"pu", "jcode"},
		Runner: func(ctx context.Context, tool, worktreeDir string, task Task, cfg RunConfig) AgentOutcome {
			observedOrder = append(observedOrder, task.ID+":"+tool)
			// Verify clean worktree starts with "bad" (no leakage from previous run).
			b, _ := os.ReadFile(filepath.Join(worktreeDir, "val.txt"))
			if strings.TrimSpace(string(b)) != "bad" {
				t.Errorf("worktree for %s:%s was dirty: %q", task.ID, tool, string(b))
			}
			_ = os.WriteFile(filepath.Join(worktreeDir, "val.txt"), []byte("good\n"), 0o644)
			wt := int64(1000)
			if tool == "pu" {
				wt = 700
			}
			return AgentOutcome{
				Status:          "completed",
				Turns:           2,
				WallTimeMS:      wt,
				InputTokens:     500,
				OutputTokens:    100,
				NotionalCostUSD: 0.001,
			}
		},
	})
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	wantOrder := []string{
		"01_task:jcode", "01_task:pu",
		"02_task:pu", "02_task:jcode",
		"03_task:jcode", "03_task:pu",
	}
	if !reflect.DeepEqual(observedOrder, wantOrder) {
		t.Fatalf("order = %v, want %v", observedOrder, wantOrder)
	}
	if len(results) != 6 {
		t.Fatalf("expected 6 results, got %d", len(results))
	}

	readBack, err := ReadResults(resultsPath)
	if err != nil || len(readBack) != 6 {
		t.Fatalf("ReadResults: len=%d err=%v", len(readBack), err)
	}
	summary := Summarize(readBack)
	if summary.Verdict != "GO" {
		t.Fatalf("expected GO verdict, got %s (%s)", summary.Verdict, summary.VerdictMsg)
	}
	reportText := FormatReport(summary)
	if !strings.Contains(reportText, "Verdict (spec §12): GO") {
		t.Fatalf("unexpected report:\n%s", reportText)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := osexec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}

func TestRateLimitDetectionExclusionAndResume(t *testing.T) {
	if !IsRateLimited("provider returned HTTP 429: Too Many Requests") {
		t.Fatalf("expected HTTP 429 to be detected as rate limited")
	}
	if !IsRateLimited(`{"error":{"type":"EntitlementError","message":"5-hour limit reached"}}`) {
		t.Fatalf("expected OpenCode Go 5-hour limit to be detected as rate limited")
	}
	if IsRateLimited("syntax error in foo.go") {
		t.Fatalf("normal error must not be flagged as rate limited")
	}

	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	_ = os.MkdirAll(repo, 0o755)
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "test")
	runGit(t, repo, "config", "user.email", "test@example.com")
	_ = os.WriteFile(filepath.Join(repo, "val.txt"), []byte("bad\n"), 0o644)
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "init")

	tasksDir := filepath.Join(tmp, "tasks")
	for _, id := range []string{"01_ok", "02_rl"} {
		d := filepath.Join(tasksDir, id)
		_ = os.MkdirAll(d, 0o755)
		toml := "id = \"" + id + "\"\nrepo = \"" + filepath.ToSlash(repo) + "\"\ncommit = \"HEAD\"\ncategory = \"bugfix\"\nprompt = \"fix\"\nacceptance = \"grep -q good val.txt\"\n"
		_ = os.WriteFile(filepath.Join(d, "task.toml"), []byte(toml), 0o644)
	}

	resultsPath := filepath.Join(tmp, "results.jsonl")
	// First run: 01_ok passes, 02_rl hits HTTP 429.
	_, err := RunAll(context.Background(), RunConfig{
		TasksDir:    tasksDir,
		ResultsPath: resultsPath,
		Model:       "deepseek-v4.1-flash",
		MaxCostUSD:  0.20,
		MaxTurns:    10,
		Timeout:     30 * time.Second,
		Tools:       []string{"pu"},
		Runner: func(ctx context.Context, tool, worktreeDir string, task Task, cfg RunConfig) AgentOutcome {
			if task.ID == "02_rl" {
				return AgentOutcome{
					Status: "failed",
					Err:    errors.New("provider returned HTTP 429: rate limit exceeded"),
				}
			}
			_ = os.WriteFile(filepath.Join(worktreeDir, "val.txt"), []byte("good\n"), 0o644)
			return AgentOutcome{
				Status:             "completed",
				WallTimeMS:         1200,
				InternalWallTimeMS: 1150,
				NotionalCostUSD:    0.001,
			}
		},
	})
	if err != nil {
		t.Fatalf("first RunAll: %v", err)
	}

	rows, err := ReadResults(resultsPath)
	if err != nil || len(rows) != 2 {
		t.Fatalf("ReadResults 1: len=%d err=%v", len(rows), err)
	}
	if !rows[1].RateLimited || rows[1].AgentStatus != "rate_limited" {
		t.Fatalf("expected 02_rl to be rate_limited, got %+v", rows[1])
	}
	if rows[0].WallTimeMS != 1200 || rows[0].InternalWallTimeMS != 1150 {
		t.Fatalf("expected external=1200 internal=1150, got %+v", rows[0])
	}
	if !rows[0].Budget.CostCapEnforced || !rows[0].Budget.TurnCapEnforced {
		t.Fatalf("expected pu budget caps recorded as enforced, got %+v", rows[0].Budget)
	}

	sum1 := Summarize(rows)
	if sum1.ByTool["pu"].Total != 1 || sum1.ByTool["pu"].Passed != 1 || sum1.ByTool["pu"].RateLimited != 1 {
		t.Fatalf("rate_limited run should be excluded from Total/Passed: %+v", sum1.ByTool["pu"])
	}
	if !strings.Contains(FormatReport(sum1), "rate_limited: 1 excluded, retryable") {
		t.Fatalf("expected rate_limited count in report:\n%s", FormatReport(sum1))
	}

	// Second run with Resume=true: must skip 01_ok and retry ONLY 02_rl.
	var retried []string
	_, err = RunAll(context.Background(), RunConfig{
		TasksDir:    tasksDir,
		ResultsPath: resultsPath,
		Model:       "deepseek-v4.1-flash",
		MaxCostUSD:  0.20,
		MaxTurns:    10,
		Timeout:     30 * time.Second,
		Tools:       []string{"pu"},
		Resume:      true,
		Runner: func(ctx context.Context, tool, worktreeDir string, task Task, cfg RunConfig) AgentOutcome {
			retried = append(retried, task.ID+":"+tool)
			_ = os.WriteFile(filepath.Join(worktreeDir, "val.txt"), []byte("good\n"), 0o644)
			return AgentOutcome{Status: "completed", WallTimeMS: 900, NotionalCostUSD: 0.001}
		},
	})
	if err != nil {
		t.Fatalf("resume RunAll: %v", err)
	}
	if !reflect.DeepEqual(retried, []string{"02_rl:pu"}) {
		t.Fatalf("resume retried = %v, want [02_rl:pu]", retried)
	}
}

func TestReportNotesJcodeMissingCostTurnCap(t *testing.T) {
	sum := Summarize([]Result{
		{
			TaskID: "t1", Tool: "jcode", Passed: true, WallTimeMS: 2000,
			Budget:        BudgetSpec{TimeoutMS: 60000, TimeoutEnforced: true, CostCapEnforced: false, TurnCapEnforced: false},
			ReasoningNote: "jcode run has no CLI reasoning-effort flag",
		},
		{
			TaskID: "t1", Tool: "pu", Passed: true, WallTimeMS: 1000,
			Budget: BudgetSpec{MaxCostUSD: 0.2, MaxTurns: 10, TimeoutMS: 60000, CostCapEnforced: true, TurnCapEnforced: true, TimeoutEnforced: true},
		},
	})
	rep := FormatReport(sum)
	if !strings.Contains(rep, "Budget note (jcode): no CLI cost/turn cap enforced") {
		t.Fatalf("expected budget note for jcode in report:\n%s", rep)
	}
	if !strings.Contains(rep, "Reasoning note (jcode):") {
		t.Fatalf("expected reasoning note for jcode in report:\n%s", rep)
	}
}

func TestJcodeMultiTurnSessionUsageParity(t *testing.T) {
	tmp := t.TempDir()
	modelsPath := filepath.Join(tmp, "models.toml")
	modelsTOML := `default = "deepseek-v4.1-flash"

[models."deepseek-v4.1-flash"]
driver = "openai-chat-compat"
model = "deepseek-v4.1-flash"
auth = "none"
context_window = 1000000
input_price_per_m = 0.15
cached_input_price_per_m = 0.003
output_price_per_m = 0.60
`
	if err := os.WriteFile(modelsPath, []byte(modelsTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fake jcode binary: writes 3 assistant turns to $JCODE_HOME/sessions/*.journal.jsonl
	// (including one duplicate message id to verify deduplication), while stdout --json
	// only emits the LAST turn's usage (matching real `jcode run --json` behavior).
	fakeBin := filepath.Join(tmp, "fake-jcode")
	script := `#!/bin/sh
set -e
mkdir -p "$JCODE_HOME/sessions"
cat <<'JSONL' > "$JCODE_HOME/sessions/session_fake.journal.jsonl"
{"append_messages":[{"id":"m1","role":"assistant","token_usage":{"prompt_tokens":10000,"input_tokens":10000,"output_tokens":100,"cache_read_input_tokens":0}}]}
{"append_messages":[{"id":"u1","role":"user","content":[{"type":"tool_result"}]}]}
{"append_messages":[{"id":"m2","role":"assistant","token_usage":{"prompt_tokens":11000,"input_tokens":11000,"output_tokens":200,"cache_read_input_tokens":9000}}]}
{"append_messages":[{"id":"m2","role":"assistant","token_usage":{"prompt_tokens":11000,"input_tokens":11000,"output_tokens":200,"cache_read_input_tokens":9000}}]}
{"append_messages":[{"id":"m3","role":"assistant","token_usage":{"prompt_tokens":12000,"input_tokens":12000,"output_tokens":300,"cache_read_input_tokens":10500}}]}
JSONL
echo '{"usage":{"input_tokens":12000,"cache_read_input_tokens":10500,"output_tokens":300}}'
`
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	out := runJcode(context.Background(), tmp, Task{ID: "fake", Prompt: "hello"}, RunConfig{
		JcodeBin:        fakeBin,
		Model:           "deepseek-v4.1-flash",
		ModelsFile:      modelsPath,
		ReasoningEffort: "provider_default",
		Timeout:         30 * time.Second,
	})
	if out.Err != nil {
		t.Fatalf("runJcode error: %v", out.Err)
	}
	if out.Turns != 3 {
		t.Fatalf("Turns = %d, want 3", out.Turns)
	}
	wantIn := 10000 + 11000 + 12000
	wantCached := 0 + 9000 + 10500
	wantOut := 100 + 200 + 300
	if out.InputTokens != wantIn || out.CachedInputTokens != wantCached || out.OutputTokens != wantOut {
		t.Fatalf("tokens = (%d, %d, %d), want (%d, %d, %d)",
			out.InputTokens, out.CachedInputTokens, out.OutputTokens, wantIn, wantCached, wantOut)
	}
	cat, err := catalog.Load(modelsPath)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := cat.Model("deepseek-v4.1-flash")
	wantCost := m.ComputeCostUSD(wantIn, wantCached, wantOut)
	if math.Abs(out.NotionalCostUSD-wantCost) > 1e-12 {
		t.Fatalf("NotionalCostUSD = %.10f, want %.10f", out.NotionalCostUSD, wantCost)
	}
	if !out.ReasoningEnforced {
		t.Fatalf("expected ReasoningEnforced=true when using provider_default parity")
	}
}
