package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ddawnlll/pu/internal/catalog"
	"github.com/ddawnlll/pu/internal/edit"
	"github.com/ddawnlll/pu/internal/plan"
	"github.com/ddawnlll/pu/internal/provider"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	write := func(p, s string) { _ = os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644) }
	write("go.mod", "module example.com/b\n\ngo 1.21\n")
	write("add.go", "package b\nfunc Add(a, b int) int { return a - b }\n")
	write("old_test.go", "package b\nimport \"testing\"\nfunc TestOld(t *testing.T) {}\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

func patchFile(t *testing.T, cp *edit.Checkpointer, dir, p, s string) {
	t.Helper()
	if err := cp.BeforeWrite(p); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644)
}

func TestAutoBaselineMarksNewTestThatFailsBeforeFix(t *testing.T) {
	dir := gitRepo(t)
	cp := edit.NewCheckpointer(dir, "s1")
	pm := plan.NewManager(dir)
	_ = pm.Update("g", []plan.Step{{ID: "S1", Title: "fix", Status: "done"}}, "")
	patchFile(t, cp, dir, "add.go", "package b\nfunc Add(a, b int) int { return a + b }\n")
	patchFile(t, cp, dir, "add_test.go", "package b\nimport \"testing\"\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal() } }\n")
	pm.MarkEdited()
	cmd := "go test -run TestAdd ."
	ev := pm.RecordEvidence(cmd, 0, 1, "", "", nil, "")
	if len(pm.OpenSteps()) != 1 {
		t.Fatal("precondition: green-only evidence must not cover")
	}
	note := autoBaseline(context.Background(), cp, pm, cmd, time.Minute, ev)
	if !strings.Contains(note, "covers the change") || len(pm.OpenSteps()) != 0 {
		t.Fatalf("baseline should mark coverage; note=%q open=%d", note, len(pm.OpenSteps()))
	}
	// The real workspace must be untouched by the baseline run.
	b, _ := os.ReadFile(filepath.Join(dir, "add.go"))
	if !strings.Contains(string(b), "a + b") {
		t.Fatal("workspace modified by baseline")
	}
}

func TestAutoBaselineDoesNotCoverTestThatAlsoPassesBefore(t *testing.T) {
	dir := gitRepo(t)
	cp := edit.NewCheckpointer(dir, "s2")
	pm := plan.NewManager(dir)
	_ = pm.Update("g", []plan.Step{{ID: "S1", Title: "fix", Status: "done"}}, "")
	patchFile(t, cp, dir, "add.go", "package b\nfunc Add(a, b int) int { return a + b }\n")
	patchFile(t, cp, dir, "noop_test.go", "package b\nimport \"testing\"\nfunc TestNoop(t *testing.T) {}\n")
	pm.MarkEdited()
	cmd := "go test -run TestNoop ."
	ev := pm.RecordEvidence(cmd, 0, 1, "", "", nil, "")
	note := autoBaseline(context.Background(), cp, pm, cmd, time.Minute, ev)
	if note != "" || len(pm.OpenSteps()) != 1 {
		t.Fatalf("test green before fix must not cover; note=%q", note)
	}
}

func TestAutoBaselineSkipsWithoutChangedTests(t *testing.T) {
	dir := gitRepo(t)
	cp := edit.NewCheckpointer(dir, "s3")
	pm := plan.NewManager(dir)
	patchFile(t, cp, dir, "add.go", "package b\nfunc Add(a, b int) int { return a + b }\n")
	ev := pm.RecordEvidence("go test .", 0, 1, "", "", nil, "")
	if note := autoBaseline(context.Background(), cp, pm, "go test .", time.Minute, ev); note != "" {
		t.Fatalf("unexpected baseline: %q", note)
	}
}

func TestImplicitGateNudgesWhenNoPlanAndUncoveredChange(t *testing.T) {
	dir := gitRepo(t)
	turn, nudges := 0, 0
	fake := fakeStreamerFunc(func(ctx context.Context, req provider.Request, _ func(provider.StreamEvent)) (*provider.Response, error) {
		turn++
		last := req.Items[len(req.Items)-1]
		if tp, ok := last.Parts[0].(provider.TextPart); ok && last.Role == provider.RoleUser && strings.Contains(tp.Text, "No passing check yet covers") {
			nudges++
		}
		if turn == 1 {
			return &provider.Response{FinishReason: "tool_calls", Item: provider.Item{Role: provider.RoleAssistant, Parts: []provider.ContentPart{
				provider.ToolCallPart{ID: "p1", Name: "patch", Arguments: `{"mode":"full_rewrite","path":"add.go","content":"package b\nfunc Add(a, b int) int { return a + b }\n"}`},
			}}}, nil
		}
		return &provider.Response{FinishReason: "stop", Item: provider.Item{Role: provider.RoleAssistant, Parts: []provider.ContentPart{provider.TextPart{Text: "done"}}}}, nil
	})
	rep, err := Run(context.Background(), Config{RepoRoot: dir, Model: catalog.Model{ID: "m"}}, fake, "fix Add")
	if err != nil {
		t.Fatal(err)
	}
	if nudges != 2 || rep.Status != StatusCompleted {
		t.Fatalf("nudges=%d status=%s, want 2 nudges then completed", nudges, rep.Status)
	}
}
