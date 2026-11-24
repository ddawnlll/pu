package core

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ddawnlll/pu/internal/edit"
	"github.com/ddawnlll/pu/internal/plan"
	"github.com/ddawnlll/pu/internal/provider"
)

const (
	compactionSoftThreshold    = 0.75
	compactionHandoffThreshold = 0.85
)

// toolResultMeta tracks metadata for each tool result in history so Stage 1
// compaction can deterministically trim superseded reads and stale exec outputs.
type toolResultMeta struct {
	toolName   string
	path       string
	readRev    int
	cmd        string
	evidenceID string
}

// estimateContextTokens returns lastInputTokens when known, or a conservative
// char/4 estimate across all items in history.
func estimateContextTokens(history []provider.Item, lastInputTokens int) int {
	est := 0
	for _, it := range history {
		for _, p := range it.Parts {
			switch v := p.(type) {
			case provider.TextPart:
				est += len(v.Text)/4 + 4
			case provider.ReasoningPart:
				est += len(v.Text)/4 + 4
			case provider.ToolCallPart:
				est += (len(v.Name)+len(v.Arguments))/4 + 8
			case provider.ToolResultPart:
				est += len(v.Content)/4 + 8
			}
		}
	}
	if lastInputTokens > est {
		return lastInputTokens
	}
	return est
}

// compactHistory applies the 2-stage KV-friendly context ceiling (spec §03):
//   - Below 75% of contextWindow: untouched (zero prefix mutation).
//   - >= 75%: Stage 1 soft trim of superseded reads and stale exec outputs.
//   - >= 85%: Stage 2 clean state handoff to fresh prefix (Layers 1–3 preserved).
//
// Returns the (possibly compacted) history and the stage applied (0, 1, or 2).
func compactHistory(
	history []provider.Item,
	prefixCount int,
	contextWindow int,
	lastInputTokens int,
	meta map[string]toolResultMeta,
	tracker *edit.Tracker,
	planMgr *plan.Manager,
	cp *edit.Checkpointer,
	userPrompt string,
) ([]provider.Item, int) {
	if contextWindow <= 0 {
		return history, 0
	}

	used := estimateContextTokens(history, lastInputTokens)
	ratio := float64(used) / float64(contextWindow)
	if ratio < compactionSoftThreshold {
		return history, 0
	}

	// Stage 1: soft trim superseded reads and stale exec outputs.
	latestReadID := make(map[string]string)
	for _, it := range history[prefixCount:] {
		if it.Role != provider.RoleTool {
			continue
		}
		for _, p := range it.Parts {
			tr, ok := p.(provider.ToolResultPart)
			if !ok {
				continue
			}
			if m, exists := meta[tr.ToolCallID]; exists && m.toolName == "read" && m.path != "" {
				latestReadID[m.path] = tr.ToolCallID
			}
		}
	}

	evState := planMgr.Snapshot()
	staleEv := make(map[string]bool)
	for _, ev := range evState.Evidence {
		if ev.Validity == "stale" {
			staleEv[ev.ID] = true
		}
	}

	trimmedAny := false
	nextHistory := make([]provider.Item, len(history))
	copy(nextHistory, history)

	for i := prefixCount; i < len(nextHistory); i++ {
		it := nextHistory[i]
		if it.Role != provider.RoleTool {
			continue
		}
		newParts := make([]provider.ContentPart, len(it.Parts))
		copy(newParts, it.Parts)
		changed := false
		for j, p := range newParts {
			tr, ok := p.(provider.ToolResultPart)
			if !ok {
				continue
			}
			m, exists := meta[tr.ToolCallID]
			if !exists {
				continue
			}
			if m.toolName == "read" && m.path != "" {
				rec, tracked := tracker.Record(m.path)
				isSuperseded := latestReadID[m.path] != tr.ToolCallID || (tracked && rec.Revision > m.readRev)
				if isSuperseded && !strings.HasPrefix(tr.Content, "[trimmed:") {
					tr.Content = fmt.Sprintf("[trimmed: superseded read of %s@rev%d]", m.path, m.readRev)
					newParts[j] = tr
					changed = true
					trimmedAny = true
				}
			} else if m.toolName == "exec" && m.evidenceID != "" && staleEv[m.evidenceID] {
				if !strings.HasPrefix(tr.Content, "[trimmed:") {
					tr.Content = fmt.Sprintf("[trimmed: stale exec output for %q (%s)]", m.cmd, m.evidenceID)
					newParts[j] = tr
					changed = true
					trimmedAny = true
				}
			}
		}
		if changed {
			it.Parts = newParts
			nextHistory[i] = it
		}
	}

	// Check whether we are at or above 85% (either before or after Stage 1).
	// If Stage 1 trimmed bytes, recompute char-based estimate; if original ratio >= 85%
	// and Stage 1 didn't bring us below 85%, perform Stage 2 clean state handoff.
	postEstimate := estimateContextTokens(nextHistory, 0)
	if !trimmedAny && lastInputTokens > postEstimate {
		postEstimate = lastInputTokens
	}
	postRatio := float64(postEstimate) / float64(contextWindow)
	if postRatio < compactionHandoffThreshold {
		if trimmedAny {
			return nextHistory, 1
		}
		return history, 0
	}

	// Stage 2: clean state handoff with fresh prefix (keep Layers 1–3 intact).
	summary := buildHandoffSummary(userPrompt, tracker, planMgr, cp)
	handoff := make([]provider.Item, 0, prefixCount+1)
	handoff = append(handoff, history[:prefixCount]...)
	handoff = append(handoff, provider.Item{
		Role:  provider.RoleUser,
		Parts: []provider.ContentPart{provider.TextPart{Text: summary}},
	})
	return handoff, 2
}

func buildHandoffSummary(userPrompt string, tracker *edit.Tracker, planMgr *plan.Manager, cp *edit.Checkpointer) string {
	var sb strings.Builder
	sb.WriteString("<state_handoff>\n")
	fmt.Fprintf(&sb, "original_request: %s\n", userPrompt)
	if planMgr.HasPlan() {
		sb.WriteString(planMgr.FormatSummary())
	} else {
		st := planMgr.Snapshot()
		if len(st.Evidence) > 0 {
			sb.WriteString("evidence:\n")
			for _, ev := range st.Evidence {
				fmt.Fprintf(&sb, "- %s (%s, exit=%d, %s): %s\n", ev.ID, ev.Validity, ev.ExitCode, ev.WorkspaceRevision, ev.Cmd)
			}
		}
	}
	if cp != nil {
		if changed := cp.ChangedFiles(); len(changed) > 0 {
			fmt.Fprintf(&sb, "files_changed: %s\n", strings.Join(changed, ", "))
		}
	}
	recs := tracker.SnapshotRecords()
	sort.Slice(recs, func(i, j int) bool { return recs[i].Path < recs[j].Path })
	if len(recs) > 0 {
		sb.WriteString("tracked_files:\n")
		for _, r := range recs {
			short := r.SHA256
			if len(short) > 12 {
				short = short[:12]
			}
			fmt.Fprintf(&sb, "- %s rev%d (%s)\n", r.Path, r.Revision, short)
		}
	}
	sb.WriteString("</state_handoff>\nContinue from the current workspace state above.")
	return sb.String()
}
