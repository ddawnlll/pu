package eval

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
