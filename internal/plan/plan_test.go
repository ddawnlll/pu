package plan

import "testing"

func done(ids ...string) []Step {
	var s []Step
	for _, id := range ids {
		s = append(s, Step{ID: id, Title: id, Status: "done"})
	}
	return s
}

func TestAlreadyGreenCheckDoesNotCoverChangedBehaviour(t *testing.T) {
	m := NewManager("")
	_ = m.Update("g", done("S1"), "")
	m.RecordEvidence("go test ./...", 0, 0, "", "", nil, "") // green before edits
	m.MarkEdited()
	m.RecordEvidence("go test ./...", 0, 1, "", "", nil, "") // still green, never red
	if len(m.OpenSteps()) != 1 {
		t.Fatal("always-green check must not close a behaviour-changing step")
	}
}

func TestRedThenGreenCoversStep(t *testing.T) {
	m := NewManager("")
	_ = m.Update("g", done("S1"), "")
	m.MarkEdited()
	m.RecordEvidence("go test -run TestNew ./x", 1, 1, "", "", nil, "")
	m.RecordEvidence("go  test -run TestNew ./x", 0, 2, "", "", nil, "")
	if n := len(m.OpenSteps()); n != 0 {
		t.Fatalf("open=%d, want 0", n)
	}
	m.InvalidateForFiles([]string{"x/a.go"})
	if len(m.OpenSteps()) != 1 {
		t.Fatal("stale evidence must reopen step")
	}
}

func TestNamedExistingCheckCoversStep(t *testing.T) {
	m := NewManager("")
	steps := done("S1")
	steps[0].Check = "go test -run TestExisting ./x"
	_ = m.Update("g", steps, "")
	m.MarkEdited()
	m.RecordEvidence("go test -run TestExisting ./x", 0, 1, "", "", nil, "")
	if len(m.OpenSteps()) != 0 {
		t.Fatal("named existing check should cover its step")
	}
}

func TestNoEditsAnyGreenCheckCounts(t *testing.T) {
	m := NewManager("")
	_ = m.Update("g", done("S1"), "")
	m.RecordEvidence("go test ./...", 0, 0, "", "", nil, "")
	if len(m.OpenSteps()) != 0 {
		t.Fatal("no-change tasks: green check is enough")
	}
}
