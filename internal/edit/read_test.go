package edit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnchorsUniqueIn1000IdenticalLines(t *testing.T) {
	lines := make([]string, 1000)
	for i := range lines {
		lines[i] = "return nil"
	}

	anchors := AnchorLines(lines)
	if len(anchors) != 1000 {
		t.Fatalf("len(anchors) = %d, want 1000", len(anchors))
	}

	seen := make(map[string]int, 1000)
	for i, a := range anchors {
		if len(a) != 5 {
			t.Fatalf("line %d anchor %q has length %d, want 5", i+1, a, len(a))
		}
		for _, ch := range a {
			if ch < 'a' || ch > 'z' {
				t.Fatalf("line %d anchor %q has non-lowercase-ASCII char %q", i+1, a, ch)
			}
		}
		if prev, dup := seen[a]; dup {
			t.Fatalf("duplicate anchor %q on lines %d and %d", a, prev, i+1)
		}
		seen[a] = i + 1
	}

	// Deterministic across repeated calls.
	anchors2 := AnchorLines(lines)
	for i := range anchors {
		if anchors[i] != anchors2[i] {
			t.Fatalf("non-deterministic anchor at line %d: %q vs %q", i+1, anchors[i], anchors2[i])
		}
	}
}

func TestTrackerReadRevisionBumpsOnChange(t *testing.T) {
	dir := t.TempDir()
	fpath := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(fpath, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tr := NewTracker(dir)
	r1, err := tr.Read("sample.txt", 1, 3)
	if err != nil {
		t.Fatalf("Read 1: %v", err)
	}
	if r1.Revision != 1 || r1.TotalLines != 3 || len(r1.Lines) != 3 {
		t.Fatalf("unexpected r1: %+v", r1)
	}

	// Reading same unmodified content keeps revision 1.
	r2, err := tr.Read("sample.txt", 2, 2)
	if err != nil {
		t.Fatalf("Read 2: %v", err)
	}
	if r2.Revision != 1 || r2.SHA256 != r1.SHA256 {
		t.Fatalf("expected same revision 1, got rev %d", r2.Revision)
	}
	if r2.Lines[0].Anchor != r1.Lines[1].Anchor {
		t.Fatalf("slice anchor mismatch: %q vs %q", r2.Lines[0].Anchor, r1.Lines[1].Anchor)
	}

	// Modifying file bumps revision.
	if err := os.WriteFile(fpath, []byte("alpha\nBETA\ngamma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile 2: %v", err)
	}
	r3, err := tr.Read("sample.txt", 1, 3)
	if err != nil {
		t.Fatalf("Read 3: %v", err)
	}
	if r3.Revision != 2 || r3.SHA256 == r1.SHA256 {
		t.Fatalf("expected bumped revision 2 and new hash, got rev %d", r3.Revision)
	}
	if r3.Lines[1].Anchor == r1.Lines[1].Anchor {
		t.Fatalf("expected line 2 anchor to change when content changed")
	}
	if !strings.Contains(r3.Format(), "rev: rev2") {
		t.Fatalf("expected formatted output to include rev2:\n%s", r3.Format())
	}
}
