package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ddawnlll/pu/internal/edit"
	"github.com/ddawnlll/pu/internal/plan"
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
	if !strings.Contains(note, "does not yet cover") || len(pm.OpenSteps()) != 1 {
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
