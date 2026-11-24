package edit

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHashlineStaleAnchorRejectedWithFreshAnchors(t *testing.T) {
	dir := t.TempDir()
	fpath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(fpath, []byte("package main\n\nfunc main() {\n\tprintln(\"v1\")\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tr := NewTracker(dir)
	r1, err := tr.Read("main.go", 1, 5)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	staleAnchor := r1.Lines[3].Anchor // line 4

	// Externally mutate line 4 so the anchor is now stale.
	if err := os.WriteFile(fpath, []byte("package main\n\nfunc main() {\n\tprintln(\"v2\")\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile v2: %v", err)
	}

	cp := NewCheckpointer(dir, "sess-stale")
	_, err = tr.Patch(cp, PatchRequest{
		Path: "main.go",
		Mode: ModeHashline,
		Ops: []HashlineOp{
			{StartLine: 4, StartAnchor: staleAnchor, NewText: "\tprintln(\"v3\")"},
		},
	})
	if err == nil {
		t.Fatalf("expected stale anchor rejection, got nil")
	}
	if !strings.Contains(err.Error(), "stale anchor") {
		t.Fatalf("expected stale anchor message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "fresh anchors for main.go") {
		t.Fatalf("expected fresh anchors in rejection, got: %v", err)
	}
	// Verify file was NOT modified.
	got, _ := os.ReadFile(fpath)
	if !strings.Contains(string(got), "v2") {
		t.Fatalf("file should remain unchanged on rejection, got:\n%s", string(got))
	}
}

func TestHashlineEditsIntendedIdenticalLine(t *testing.T) {
	dir := t.TempDir()
	content := "return nil\nreturn nil\nreturn nil\nreturn nil\nreturn nil\n"
	fpath := filepath.Join(dir, "dup.go")
	if err := os.WriteFile(fpath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tr := NewTracker(dir)
	r1, err := tr.Read("dup.go", 1, 5)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// Edit specifically line 4 of 5 identical lines.
	cp := NewCheckpointer(dir, "sess-dup")
	res, err := tr.Patch(cp, PatchRequest{
		Path: "dup.go",
		Mode: ModeHashline,
		Ops: []HashlineOp{
			{StartLine: 4, StartAnchor: r1.Lines[3].Anchor, NewText: "return err"},
		},
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !strings.Contains(res.Diff, "+4:") || !strings.Contains(res.Diff, "return err") {
		t.Fatalf("expected fresh-anchored diff for line 4, got:\n%s", res.Diff)
	}

	gotBytes, _ := os.ReadFile(fpath)
	gotLines := splitLines(string(gotBytes))
	for i, want := range []string{"return nil", "return nil", "return nil", "return err", "return nil"} {
		if gotLines[i] != want {
			t.Fatalf("line %d = %q, want %q", i+1, gotLines[i], want)
		}
	}
}

func TestSearchReplaceRejectsZeroOrMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	fpath := filepath.Join(dir, "sr.go")
	if err := os.WriteFile(fpath, []byte("foo\nbar\nfoo\nbaz\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tr := NewTracker(dir)
	cp := NewCheckpointer(dir, "sess-sr")

	// 0 matches rejected
	if _, err := tr.Patch(cp, PatchRequest{
		Path:    "sr.go",
		Mode:    ModeSearchReplace,
		OldText: "missing",
		NewText: "x",
	}); err == nil || !strings.Contains(err.Error(), "0 matches") {
		t.Fatalf("expected 0 matches rejection, got %v", err)
	}

	// 2 matches rejected
	if _, err := tr.Patch(cp, PatchRequest{
		Path:    "sr.go",
		Mode:    ModeSearchReplace,
		OldText: "foo",
		NewText: "qux",
	}); err == nil || !strings.Contains(err.Error(), "2 matches") {
		t.Fatalf("expected 2 matches rejection, got %v", err)
	}

	// Exactly 1 match succeeds
	res, err := tr.Patch(cp, PatchRequest{
		Path:    "sr.go",
		Mode:    ModeSearchReplace,
		OldText: "bar",
		NewText: "BAR",
	})
	if err != nil {
		t.Fatalf("expected 1 match to succeed, got %v", err)
	}
	if res.Revision < 1 {
		t.Fatalf("expected revision >= 1, got %d", res.Revision)
	}
}

func TestDirtyGitTreeUndoRestoresUncommittedAndLeavesUntouchedAlone(t *testing.T) {
	dir := t.TempDir()
	runGitCmd(t, dir, "init")
	runGitCmd(t, dir, "config", "user.name", "test")
	runGitCmd(t, dir, "config", "user.email", "test@example.com")

	editedPath := filepath.Join(dir, "edited.txt")
	untouchedPath := filepath.Join(dir, "untouched.txt")
	_ = os.WriteFile(editedPath, []byte("committed-edited\n"), 0o644)
	_ = os.WriteFile(untouchedPath, []byte("committed-untouched\n"), 0o644)
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-m", "initial")

	// User has uncommitted dirty edits in BOTH files before pu runs.
	userEditedContent := "user-uncommitted-edited\n"
	userUntouchedContent := "user-uncommitted-untouched\n"
	_ = os.WriteFile(editedPath, []byte(userEditedContent), 0o644)
	_ = os.WriteFile(untouchedPath, []byte(userUntouchedContent), 0o644)

	tr := NewTracker(dir)
	cp := NewCheckpointer(dir, "sess-dirty")

	// pu edits edited.txt TWICE in the same session.
	if _, err := tr.Patch(cp, PatchRequest{
		Path:    "edited.txt",
		Mode:    ModeFullRewrite,
		Content: "pu-change-1\n",
	}); err != nil {
		t.Fatalf("patch 1: %v", err)
	}
	if _, err := tr.Patch(cp, PatchRequest{
		Path:    "edited.txt",
		Mode:    ModeFullRewrite,
		Content: "pu-change-2\n",
	}); err != nil {
		t.Fatalf("patch 2: %v", err)
	}

	diffSummary, err := DiffSession(dir, "")
	if err != nil {
		t.Fatalf("DiffSession: %v", err)
	}
	if !strings.Contains(diffSummary, "M edited.txt") {
		t.Fatalf("unexpected DiffSession output:\n%s", diffSummary)
	}

	// Undo must restore user's uncommitted version of edited.txt and leave untouched.txt alone.
	if _, err := UndoSession(dir, ""); err != nil {
		t.Fatalf("UndoSession: %v", err)
	}

	gotEdited, _ := os.ReadFile(editedPath)
	gotUntouched, _ := os.ReadFile(untouchedPath)
	if string(gotEdited) != userEditedContent {
		t.Fatalf("edited.txt = %q, want user's uncommitted %q", string(gotEdited), userEditedContent)
	}
	if string(gotUntouched) != userUntouchedContent {
		t.Fatalf("untouched.txt = %q, want %q", string(gotUntouched), userUntouchedContent)
	}
}

func TestCreateDeleteRoundTripThroughUndo(t *testing.T) {
	dir := t.TempDir()
	existingPath := filepath.Join(dir, "existing.txt")
	_ = os.WriteFile(existingPath, []byte("keep me\n"), 0o644)

	tr := NewTracker(dir)
	cp := NewCheckpointer(dir, "sess-cd")

	// Create a new file and delete an existing file.
	if _, err := tr.Patch(cp, PatchRequest{
		Path:    "created.txt",
		Mode:    ModeCreate,
		Content: "brand new\n",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := tr.Patch(cp, PatchRequest{
		Path: "existing.txt",
		Mode: ModeDelete,
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "created.txt")); err != nil {
		t.Fatalf("expected created.txt to exist: %v", err)
	}
	if _, err := os.Stat(existingPath); !os.IsNotExist(err) {
		t.Fatalf("expected existing.txt to be deleted")
	}

	// Undo should remove created.txt and restore existing.txt.
	if _, err := UndoSession(dir, "sess-cd"); err != nil {
		t.Fatalf("UndoSession: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected created.txt to be removed by undo")
	}
	got, err := os.ReadFile(existingPath)
	if err != nil || string(got) != "keep me\n" {
		t.Fatalf("expected existing.txt restored to 'keep me\\n', got %q err=%v", string(got), err)
	}
}

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := osexec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}
