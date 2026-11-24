package edit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const MaxDiffBytes = 50 * 1024 // 50 KiB cap per spec §06 & CLAUDE.md M3

// PatchMode selects how a patch is applied.
type PatchMode string

const (
	ModeHashline      PatchMode = "hashline"
	ModeSearchReplace PatchMode = "search_replace"
	ModeFullRewrite   PatchMode = "full_rewrite"
	ModeCreate        PatchMode = "create"
	ModeDelete        PatchMode = "delete"
)

// HashlineOp specifies one line or line-range edit anchored by 5-letter anchors.
type HashlineOp struct {
	StartLine   int    `json:"start_line"`
	StartAnchor string `json:"start_anchor"`
	EndLine     int    `json:"end_line,omitempty"`
	EndAnchor   string `json:"end_anchor,omitempty"`
	NewText     string `json:"new_text"`
	InsertAfter bool   `json:"insert_after,omitempty"`
}

// PatchRequest describes a file edit, creation, or deletion.
type PatchRequest struct {
	Path           string       `json:"path"`
	Mode           PatchMode    `json:"mode,omitempty"`
	ExpectedSHA256 string       `json:"expected_sha256,omitempty"`
	Ops            []HashlineOp `json:"ops,omitempty"`
	OldText        string       `json:"old_text,omitempty"`
	NewText        string       `json:"new_text,omitempty"`
	Content        string       `json:"content,omitempty"`
}

// PatchResult is returned on a successful patch with a fresh-anchored diff.
type PatchResult struct {
	Path     string    `json:"path"`
	Mode     PatchMode `json:"mode"`
	Revision int       `json:"revision"`
	SHA256   string    `json:"sha256"`
	Diff     string    `json:"diff"`
}

// Format renders PatchResult for the model.
func (r *PatchResult) Format() string {
	shortHash := r.SHA256
	if len(shortHash) > 12 {
		shortHash = shortHash[:12]
	}
	if r.Mode == ModeDelete {
		return fmt.Sprintf("deleted %s (rev: rev%d)\n", r.Path, r.Revision)
	}
	return fmt.Sprintf("patched %s (mode: %s, rev: rev%d, sha256: %s)\n%s",
		r.Path, r.Mode, r.Revision, shortHash, r.Diff)
}

// Patch applies an edit using strict revision/anchor locking (no fuzzy matching),
// snapshots the file via cp before the first write in the session, bumps the
// tracker revision, and returns a fresh-anchored diff (<=50 KiB).
func (t *Tracker) Patch(cp *Checkpointer, req PatchRequest) (*PatchResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return nil, errors.New("patch: path is required")
	}
	if req.Mode == "" {
		req.Mode = ModeHashline
	}

	absPath := req.Path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(t.repoRoot, req.Path)
	}
	absPath = filepath.Clean(absPath)

	relPath, err := filepath.Rel(t.repoRoot, absPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return nil, fmt.Errorf("patch: path %q is outside repo root", req.Path)
	}
	relPath = filepath.ToSlash(relPath)

	switch req.Mode {
	case ModeCreate:
		return t.patchCreate(cp, absPath, relPath, req)
	case ModeDelete:
		return t.patchDelete(cp, absPath, relPath, req)
	case ModeHashline:
		return t.patchHashline(cp, absPath, relPath, req)
	case ModeSearchReplace:
		return t.patchSearchReplace(cp, absPath, relPath, req)
	case ModeFullRewrite:
		return t.patchFullRewrite(cp, absPath, relPath, req)
	default:
		return nil, fmt.Errorf("patch: unsupported mode %q", req.Mode)
	}
}

func (t *Tracker) patchCreate(cp *Checkpointer, absPath, relPath string, req PatchRequest) (*PatchResult, error) {
	if _, err := os.Stat(absPath); err == nil {
		return nil, fmt.Errorf("patch create rejected: %s already exists", relPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if cp != nil {
		if err := cp.BeforeWrite(relPath); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, fmt.Errorf("patch create mkdir: %w", err)
	}
	if err := os.WriteFile(absPath, []byte(req.Content), 0o644); err != nil {
		return nil, fmt.Errorf("patch create write: %w", err)
	}
	sum := sha256.Sum256([]byte(req.Content))
	hashHex := hex.EncodeToString(sum[:])
	rev := t.recordHash(relPath, hashHex)
	newLines := splitLines(req.Content)
	diff := formatFreshAnchoredDiff(nil, newLines, AnchorLines(newLines))
	return &PatchResult{
		Path:     relPath,
		Mode:     ModeCreate,
		Revision: rev,
		SHA256:   hashHex,
		Diff:     diff,
	}, nil
}

func (t *Tracker) patchDelete(cp *Checkpointer, absPath, relPath string, req PatchRequest) (*PatchResult, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("patch delete rejected: %w", err)
	}
	curHash := sha256Hex(data)
	if err := checkExpectedHash(relPath, curHash, req.ExpectedSHA256, splitLines(string(data)), 1); err != nil {
		return nil, err
	}
	if cp != nil {
		if err := cp.BeforeWrite(relPath); err != nil {
			return nil, err
		}
	}
	if err := os.Remove(absPath); err != nil {
		return nil, fmt.Errorf("patch delete remove: %w", err)
	}
	rev := t.recordHash(relPath, "deleted")
	return &PatchResult{
		Path:     relPath,
		Mode:     ModeDelete,
		Revision: rev,
		SHA256:   "",
		Diff:     fmt.Sprintf("--- %s\n+++ /dev/null\n", relPath),
	}, nil
}

func (t *Tracker) patchFullRewrite(cp *Checkpointer, absPath, relPath string, req PatchRequest) (*PatchResult, error) {
	oldData, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("patch full_rewrite: %w", err)
	}
	oldHash := sha256Hex(oldData)
	oldLines := splitLines(string(oldData))
	if err := checkExpectedHash(relPath, oldHash, req.ExpectedSHA256, oldLines, 1); err != nil {
		return nil, err
	}
	if cp != nil {
		if err := cp.BeforeWrite(relPath); err != nil {
			return nil, err
		}
	}
	if err := writePreserveMode(absPath, []byte(req.Content)); err != nil {
		return nil, err
	}
	newHash := sha256Hex([]byte(req.Content))
	rev := t.recordHash(relPath, newHash)
	newLines := splitLines(req.Content)
	diff := formatFreshAnchoredDiff(oldLines, newLines, AnchorLines(newLines))
	return &PatchResult{
		Path:     relPath,
		Mode:     ModeFullRewrite,
		Revision: rev,
		SHA256:   newHash,
		Diff:     diff,
	}, nil
}

func (t *Tracker) patchSearchReplace(cp *Checkpointer, absPath, relPath string, req PatchRequest) (*PatchResult, error) {
	if req.OldText == "" {
		return nil, errors.New("patch search_replace: old_text is required")
	}
	oldData, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("patch search_replace: %w", err)
	}
	oldText := string(oldData)
	oldHash := sha256Hex(oldData)
	oldLines := splitLines(oldText)
	if err := checkExpectedHash(relPath, oldHash, req.ExpectedSHA256, oldLines, 1); err != nil {
		return nil, err
	}

	count := strings.Count(oldText, req.OldText)
	if count == 0 {
		return nil, fmt.Errorf("patch search_replace rejected: 0 matches for old_text in %s (no fuzzy matching)\n%s",
			relPath, formatFreshRange(relPath, oldHash, oldLines, 1, min(len(oldLines), 40)))
	}
	if count > 1 {
		// Report fresh anchors around the first occurrence to help model disambiguate.
		firstIdx := strings.Index(oldText, req.OldText)
		firstLine := strings.Count(oldText[:firstIdx], "\n") + 1
		return nil, fmt.Errorf("patch search_replace rejected: %d matches for old_text in %s (must match exactly once)\n%s",
			count, relPath, formatFreshRange(relPath, oldHash, oldLines, max(1, firstLine-3), min(len(oldLines), firstLine+10)))
	}

	newText := strings.Replace(oldText, req.OldText, req.NewText, 1)
	if cp != nil {
		if err := cp.BeforeWrite(relPath); err != nil {
			return nil, err
		}
	}
	if err := writePreserveMode(absPath, []byte(newText)); err != nil {
		return nil, err
	}
	newHash := sha256Hex([]byte(newText))
	rev := t.recordHash(relPath, newHash)
	newLines := splitLines(newText)
	diff := formatFreshAnchoredDiff(oldLines, newLines, AnchorLines(newLines))
	return &PatchResult{
		Path:     relPath,
		Mode:     ModeSearchReplace,
		Revision: rev,
		SHA256:   newHash,
		Diff:     diff,
	}, nil
}

func (t *Tracker) patchHashline(cp *Checkpointer, absPath, relPath string, req PatchRequest) (*PatchResult, error) {
	if len(req.Ops) == 0 {
		return nil, errors.New("patch hashline: ops is required")
	}
	oldData, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("patch hashline: %w", err)
	}
	oldStr := string(oldData)
	hasTrailingNL := strings.HasSuffix(oldStr, "\n")
	oldHash := sha256Hex(oldData)
	oldLines := splitLines(oldStr)
	curAnchors := AnchorLines(oldLines)

	if err := checkExpectedHash(relPath, oldHash, req.ExpectedSHA256, oldLines, req.Ops[0].StartLine); err != nil {
		return nil, err
	}

	// Normalize and strictly validate all ops before mutating anything.
	type resolvedOp struct {
		start       int // 1-based inclusive
		end         int // 1-based inclusive
		insertAfter bool
		newLines    []string
	}
	resolved := make([]resolvedOp, len(req.Ops))

	for i, op := range req.Ops {
		if op.StartLine < 1 || op.StartLine > len(oldLines) {
			return nil, fmt.Errorf("patch hashline rejected: op[%d] start_line %d out of range (1..%d)\n%s",
				i, op.StartLine, len(oldLines),
				formatFreshRange(relPath, oldHash, oldLines, max(1, len(oldLines)-15), len(oldLines)))
		}
		if curAnchors[op.StartLine-1] != op.StartAnchor {
			return nil, fmt.Errorf("patch hashline rejected: stale anchor at line %d (got %q, current %q; no fuzzy matching)\n%s",
				op.StartLine, op.StartAnchor, curAnchors[op.StartLine-1],
				formatFreshRange(relPath, oldHash, oldLines, max(1, op.StartLine-5), min(len(oldLines), op.StartLine+5)))
		}

		endLine := op.EndLine
		if endLine == 0 {
			endLine = op.StartLine
		}
		if endLine < op.StartLine || endLine > len(oldLines) {
			return nil, fmt.Errorf("patch hashline rejected: op[%d] end_line %d out of range (%d..%d)\n%s",
				i, endLine, op.StartLine, len(oldLines),
				formatFreshRange(relPath, oldHash, oldLines, max(1, op.StartLine-5), min(len(oldLines), op.StartLine+10)))
		}
		if endLine != op.StartLine {
			if op.EndAnchor == "" || curAnchors[endLine-1] != op.EndAnchor {
				return nil, fmt.Errorf("patch hashline rejected: stale end_anchor at line %d (got %q, current %q; no fuzzy matching)\n%s",
					endLine, op.EndAnchor, curAnchors[endLine-1],
					formatFreshRange(relPath, oldHash, oldLines, max(1, op.StartLine-3), min(len(oldLines), endLine+5)))
			}
		}

		var repl []string
		if op.NewText != "" {
			repl = splitLines(op.NewText)
		}
		resolved[i] = resolvedOp{
			start:       op.StartLine,
			end:         endLine,
			insertAfter: op.InsertAfter,
			newLines:    repl,
		}
	}

	// Sort by start ascending to check overlaps.
	sort.Slice(resolved, func(i, j int) bool {
		if resolved[i].start != resolved[j].start {
			return resolved[i].start < resolved[j].start
		}
		return resolved[i].end < resolved[j].end
	})
	for i := 1; i < len(resolved); i++ {
		if resolved[i].start <= resolved[i-1].end {
			return nil, fmt.Errorf("patch hashline rejected: overlapping ops (%d-%d and %d-%d)",
				resolved[i-1].start, resolved[i-1].end, resolved[i].start, resolved[i].end)
		}
	}

	// Apply bottom-up so earlier line indices stay valid.
	newLines := make([]string, len(oldLines))
	copy(newLines, oldLines)
	for i := len(resolved) - 1; i >= 0; i-- {
		op := resolved[i]
		if op.insertAfter {
			idx := op.end // insert after 1-based end
			tail := append([]string(nil), newLines[idx:]...)
			newLines = append(newLines[:idx], append(op.newLines, tail...)...)
		} else {
			tail := append([]string(nil), newLines[op.end:]...)
			newLines = append(newLines[:op.start-1], append(op.newLines, tail...)...)
		}
	}

	newStr := strings.Join(newLines, "\n")
	if len(newLines) > 0 && (hasTrailingNL || len(oldLines) == 0) {
		newStr += "\n"
	}

	if cp != nil {
		if err := cp.BeforeWrite(relPath); err != nil {
			return nil, err
		}
	}
	if err := writePreserveMode(absPath, []byte(newStr)); err != nil {
		return nil, err
	}

	newHash := sha256Hex([]byte(newStr))
	rev := t.recordHash(relPath, newHash)
	freshAnchors := AnchorLines(newLines)
	diff := formatFreshAnchoredDiff(oldLines, newLines, freshAnchors)

	return &PatchResult{
		Path:     relPath,
		Mode:     ModeHashline,
		Revision: rev,
		SHA256:   newHash,
		Diff:     diff,
	}, nil
}

func checkExpectedHash(relPath, curHash, expected string, lines []string, focusLine int) error {
	if expected == "" {
		return nil
	}
	exp := strings.ToLower(strings.TrimSpace(expected))
	if exp == curHash || (len(exp) >= 8 && strings.HasPrefix(curHash, exp)) {
		return nil
	}
	if focusLine < 1 {
		focusLine = 1
	}
	return fmt.Errorf("patch rejected: stale file hash for %s (got %q, current %q)\n%s",
		relPath, expected, curHash[:12],
		formatFreshRange(relPath, curHash, lines, max(1, focusLine-5), min(len(lines), focusLine+15)))
}

func formatFreshRange(relPath, curHash string, lines []string, start, end int) string {
	var sb strings.Builder
	short := curHash
	if len(short) > 12 {
		short = short[:12]
	}
	fmt.Fprintf(&sb, "fresh anchors for %s (sha256: %s, lines %d-%d/%d):\n", relPath, short, start, end, len(lines))
	if len(lines) == 0 || start > end {
		return sb.String()
	}
	anchors := AnchorLines(lines)
	for i := start; i <= end && i <= len(lines); i++ {
		fmt.Fprintf(&sb, "%d:%s| %s\n", i, anchors[i-1], lines[i-1])
	}
	return sb.String()
}

func formatFreshAnchoredDiff(oldLines, newLines, freshAnchors []string) string {
	// Find common prefix and suffix to emit a compact fresh-anchored hunk.
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	oldEnd := len(oldLines)
	newEnd := len(newLines)
	for oldEnd > prefix && newEnd > prefix && oldLines[oldEnd-1] == newLines[newEnd-1] {
		oldEnd--
		newEnd--
	}

	const ctxLines = 2
	ctxStart := max(0, prefix-ctxLines)
	ctxNewEnd := min(len(newLines), newEnd+ctxLines)

	var sb strings.Builder
	for i := ctxStart; i < prefix; i++ {
		fmt.Fprintf(&sb, " %d:%s| %s\n", i+1, freshAnchors[i], newLines[i])
	}
	for i := prefix; i < oldEnd; i++ {
		fmt.Fprintf(&sb, "-%d| %s\n", i+1, oldLines[i])
	}
	for i := prefix; i < newEnd; i++ {
		fmt.Fprintf(&sb, "+%d:%s| %s\n", i+1, freshAnchors[i], newLines[i])
	}
	for i := newEnd; i < ctxNewEnd; i++ {
		fmt.Fprintf(&sb, " %d:%s| %s\n", i+1, freshAnchors[i], newLines[i])
	}

	out := sb.String()
	if len(out) > MaxDiffBytes {
		cut := MaxDiffBytes - 64
		for cut > 0 && (out[cut]&0xc0) == 0x80 {
			cut--
		}
		out = out[:cut] + "\n... [diff truncated at 50 KiB]\n"
	}
	return out
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func writePreserveMode(absPath string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(absPath); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(absPath, data, mode)
}
