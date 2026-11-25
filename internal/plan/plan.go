package plan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Step is one work item in .pu/plan.json (spec §05).
type Step struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Status    string   `json:"status"` // "pending", "in_progress", "done", "blocked"
	Files     []string `json:"files,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	// Check optionally names an existing test command that covers this step.
	Check string `json:"check,omitempty"`
}

// Evidence binds a verification command and exit code to a workspace revision.
type Evidence struct {
	ID                string   `json:"id"`
	Step              string   `json:"step,omitempty"`
	Kind              string   `json:"kind,omitempty"`
	Cmd               string   `json:"cmd"`
	ExitCode          int      `json:"exit_code"`
	WorkspaceRevision string   `json:"workspace_revision"`
	RevisionNum       int      `json:"revision_num"`
	Files             []string `json:"files,omitempty"`
	OutputRef         string   `json:"output_ref,omitempty"`
	Validity          string   `json:"validity"` // "current", "stale", "failed"
	// Covers is true when this passing check exercises changed behaviour: the
	// same command failed earlier in the session (red -> green) after or before edits.
	Covers bool `json:"covers,omitempty"`
}

// State is the serialized shape of .pu/plan.json (spec §05).
type State struct {
	Goal     string     `json:"goal"`
	Mode     string     `json:"mode"`
	Steps    []Step     `json:"steps"`
	Evidence []Evidence `json:"evidence"`
	Status   string     `json:"status"` // "in_progress", "completed", "blocked", "budget_exhausted", "interrupted", "failed"
}

// Manager holds the in-memory plan and mirrors it to .pu/plan.json.
type Manager struct {
	mu       sync.Mutex
	repoRoot string
	state    State
	hasPlan  bool
	nextEvID int
	edited   bool
	seenRed  map[string]bool
}

func normCmd(c string) string { return strings.Join(strings.Fields(c), " ") }

// MarkEdited records that the agent changed workspace files. Once set, done
// steps need evidence that covers the changed behaviour.
func (m *Manager) MarkEdited() {
	m.mu.Lock()
	m.edited = true
	m.mu.Unlock()
}

// NewManager creates a plan manager rooted at repoRoot.
func NewManager(repoRoot string) *Manager {
	return &Manager{
		repoRoot: repoRoot,
		state: State{
			Mode:   "auto",
			Status: "in_progress",
		},
	}
}

// HasPlan reports whether the model has explicitly created/updated a plan.
func (m *Manager) HasPlan() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hasPlan
}

// Update replaces the plan goal/steps/status and mirrors to .pu/plan.json.
func (m *Manager) Update(goal string, steps []Step, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if goal != "" {
		m.state.Goal = goal
	}
	if steps != nil {
		m.state.Steps = steps
	}
	if status != "" {
		m.state.Status = status
	}
	m.hasPlan = true
	return m.persistLocked()
}

// SetStatus updates the terminal status and mirrors to .pu/plan.json if a plan exists.
func (m *Manager) SetStatus(status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Status = status
	if m.hasPlan || len(m.state.Evidence) > 0 {
		_ = m.persistLocked()
	}
}

// MarkFailedBefore records that evidence evID's command was shown to fail on
// the pre-edit code (pu ran it itself against the checkpoint baseline), so the
// passing result covers the changed behaviour just like a red -> green pair.
func (m *Manager) MarkFailedBefore(evID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.state.Evidence {
		ev := &m.state.Evidence[i]
		if ev.ID == evID && ev.ExitCode == 0 {
			if m.seenRed == nil {
				m.seenRed = map[string]bool{}
			}
			m.seenRed[normCmd(ev.Cmd)] = true
			ev.Covers = true
			if m.hasPlan {
				_ = m.persistLocked()
			}
			return
		}
	}
}

// SeenRed reports whether cmd has already failed in this session.
func (m *Manager) SeenRed(cmd string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seenRed[normCmd(cmd)]
}

// RecordEvidence adds a verification evidence record bound to revNum.
func (m *Manager) RecordEvidence(cmd string, exitCode, revNum int, stepID, kind string, files []string, outputRef string) Evidence {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextEvID++
	validity := "current"
	nc := normCmd(cmd)
	if m.seenRed == nil {
		m.seenRed = map[string]bool{}
	}
	if exitCode != 0 {
		validity = "failed"
		m.seenRed[nc] = true
	}
	covers := exitCode == 0 && m.seenRed[nc]
	ev := Evidence{
		ID:                fmt.Sprintf("E%d", m.nextEvID),
		Step:              stepID,
		Kind:              kind,
		Cmd:               cmd,
		ExitCode:          exitCode,
		WorkspaceRevision: fmt.Sprintf("rev%d", revNum),
		RevisionNum:       revNum,
		Files:             slices.Clone(files),
		OutputRef:         outputRef,
		Validity:          validity,
		Covers:            covers,
	}
	m.state.Evidence = append(m.state.Evidence, ev)
	if m.hasPlan {
		_ = m.persistLocked()
	}
	return ev
}

// InvalidateForFiles marks any current evidence as stale when any covered file
// (or any file, if the evidence covers the whole workspace) changes revision.
func (m *Manager) InvalidateForFiles(changedPaths []string) int {
	if len(changedPaths) == 0 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	changedSet := make(map[string]bool, len(changedPaths))
	for _, p := range changedPaths {
		changedSet[filepath.ToSlash(p)] = true
	}

	staled := 0
	for i := range m.state.Evidence {
		ev := &m.state.Evidence[i]
		if ev.Validity != "current" {
			continue
		}
		if len(ev.Files) == 0 {
			// Workspace-wide check: any modified file stales it.
			ev.Validity = "stale"
			staled++
			continue
		}
		for _, f := range ev.Files {
			if changedSet[filepath.ToSlash(f)] {
				ev.Validity = "stale"
				staled++
				break
			}
		}
	}
	if staled > 0 && m.hasPlan {
		_ = m.persistLocked()
	}
	return staled
}

// OpenSteps returns plan steps that are not "done" or lack current passing
// evidence. After the agent edited files, evidence counts only if it covers the
// changed behaviour: a red -> green check, or the step's named Check command.
// A check that was only ever green (e.g. before any edit) does not count.
func (m *Manager) OpenSteps() []Step {
	m.mu.Lock()
	defer m.mu.Unlock()
	steps := m.state.Steps
	if !m.hasPlan || len(steps) == 0 {
		return nil
	}

	ok := func(ev Evidence, s Step) bool {
		if ev.Validity != "current" || ev.ExitCode != 0 {
			return false
		}
		if ev.Step != "" && ev.Step != s.ID {
			return false
		}
		if !m.edited {
			return true
		}
		return ev.Covers || (s.Check != "" && normCmd(s.Check) == normCmd(ev.Cmd))
	}

	var open []Step
	for _, s := range steps {
		if s.Status != "done" {
			open = append(open, s)
			continue
		}
		covered := false
		for _, ev := range m.state.Evidence {
			if ok(ev, s) {
				covered = true
				break
			}
		}
		if !covered {
			open = append(open, s)
		}
	}
	return open
}

// IsBlocked reports whether the plan status or any step is explicitly blocked.
func (m *Manager) IsBlocked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Status == "blocked" {
		return true
	}
	for _, s := range m.state.Steps {
		if s.Status == "blocked" {
			return true
		}
	}
	return false
}

// Snapshot returns a deep-enough copy of the current plan state.
func (m *Manager) Snapshot() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := m.state
	cp.Steps = slices.Clone(m.state.Steps)
	cp.Evidence = slices.Clone(m.state.Evidence)
	return cp
}

// FormatSummary returns a compact text representation of the plan for the model.
func (m *Manager) FormatSummary() string {
	st := m.Snapshot()
	var sb strings.Builder
	fmt.Fprintf(&sb, "plan goal: %s (status: %s)\n", st.Goal, st.Status)
	for _, s := range st.Steps {
		fmt.Fprintf(&sb, "- [%s] %s: %s\n", s.Status, s.ID, s.Title)
	}
	if len(st.Evidence) > 0 {
		sb.WriteString("evidence:\n")
		for _, ev := range st.Evidence {
			fmt.Fprintf(&sb, "- %s (%s, exit=%d, %s): %s\n", ev.ID, ev.Validity, ev.ExitCode, ev.WorkspaceRevision, ev.Cmd)
		}
	}
	return sb.String()
}

func (m *Manager) persistLocked() error {
	if m.repoRoot == "" {
		return nil
	}
	dir := filepath.Join(m.repoRoot, ".pu")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "plan.json"), data, 0o644)
}
