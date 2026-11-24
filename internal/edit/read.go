package edit

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AnchoredLine is a single line with its 1-based line number and unique 5-letter anchor.
type AnchoredLine struct {
	Number int    `json:"line"`
	Anchor string `json:"anchor"`
	Text   string `json:"text"`
}

// ReadResult is the structured output of an anchored file read.
type ReadResult struct {
	Path       string         `json:"path"`
	Revision   int            `json:"revision"`
	SHA256     string         `json:"sha256"`
	TotalLines int            `json:"total_lines"`
	StartLine  int            `json:"start_line"`
	EndLine    int            `json:"end_line"`
	Truncated  bool           `json:"truncated"`
	Lines      []AnchoredLine `json:"lines"`
}

// Format renders ReadResult in compact anchored form for the model.
func (r *ReadResult) Format() string {
	var sb strings.Builder
	shortHash := r.SHA256
	if len(shortHash) > 12 {
		shortHash = shortHash[:12]
	}
	fmt.Fprintf(&sb, "file: %s rev: rev%d sha256: %s lines: %d-%d/%d",
		r.Path, r.Revision, shortHash, r.StartLine, r.EndLine, r.TotalLines)
	if r.Truncated {
		sb.WriteString(" (truncated)")
	}
	sb.WriteByte('\n')
	for _, l := range r.Lines {
		fmt.Fprintf(&sb, "%d:%s| %s\n", l.Number, l.Anchor, l.Text)
	}
	return sb.String()
}

// FileRecord binds a tracked file path to its latest observed SHA-256, stat, and revision.
type FileRecord struct {
	Path     string
	AbsPath  string
	SHA256   string
	Revision int
	Size     int64
	ModTime  time.Time
}

// Tracker maintains per-file SHA-256 hashes and monotonic revisions across a session.
type Tracker struct {
	mu       sync.Mutex
	repoRoot string
	nextRev  int
	files    map[string]*FileRecord
}

// NewTracker creates a file revision tracker rooted at repoRoot.
func NewTracker(repoRoot string) *Tracker {
	absRoot, err := filepath.Abs(repoRoot)
	if err == nil {
		repoRoot = absRoot
	}
	return &Tracker{
		repoRoot: repoRoot,
		files:    make(map[string]*FileRecord),
	}
}

// AnchorLines generates deterministic, tokenizer-friendly 5-letter lowercase ASCII
// anchors ([a-z]{5}) that are guaranteed unique within the file, even when all
// lines are byte-identical.
func AnchorLines(lines []string) []string {
	anchors := make([]string, len(lines))
	used := make(map[string]struct{}, len(lines))

	for idx, line := range lines {
		lineNum := idx + 1
		var anchor string
		for attempt := 0; ; attempt++ {
			var sum [32]byte
			if attempt == 0 {
				sum = sha256.Sum256([]byte(fmt.Sprintf("%d:%s", lineNum, line)))
			} else {
				sum = sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", lineNum, attempt, line)))
			}
			v := binary.BigEndian.Uint32(sum[:4])
			var buf [5]byte
			for j := 0; j < 5; j++ {
				buf[j] = 'a' + byte(v%26)
				v /= 26
			}
			cand := string(buf[:])
			if _, exists := used[cand]; !exists {
				anchor = cand
				used[cand] = struct{}{}
				break
			}
		}
		anchors[idx] = anchor
	}
	return anchors
}

// Read reads a file (optionally bounded by 1-based startLine..endLine), binds
// its SHA-256 hash to a revision in the tracker, and returns unique 5-letter
// line anchors.
func (t *Tracker) Read(path string, startLine, endLine int) (*ReadResult, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("read: path is required")
	}

	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(t.repoRoot, path)
	}
	absPath = filepath.Clean(absPath)

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	sum := sha256.Sum256(data)
	hashHex := hex.EncodeToString(sum[:])

	displayPath := absPath
	if rel, err := filepath.Rel(t.repoRoot, absPath); err == nil && !strings.HasPrefix(rel, "..") {
		displayPath = filepath.ToSlash(rel)
	}

	rev := t.recordHashWithPath(displayPath, absPath, hashHex)

	rawLines := splitLines(string(data))
	total := len(rawLines)

	if total == 0 {
		return &ReadResult{
			Path:       displayPath,
			Revision:   rev,
			SHA256:     hashHex,
			TotalLines: 0,
			StartLine:  0,
			EndLine:    0,
		}, nil
	}

	if startLine <= 0 {
		startLine = 1
	}
	if startLine > total {
		return nil, fmt.Errorf("read %s: start_line %d exceeds file length %d", displayPath, startLine, total)
	}

	const defaultWindow = 500
	truncated := false
	if endLine <= 0 {
		endLine = startLine + defaultWindow - 1
		if endLine < total {
			truncated = true
		}
	}
	if endLine > total {
		endLine = total
	}
	if endLine < startLine {
		return nil, fmt.Errorf("read %s: end_line %d < start_line %d", displayPath, endLine, startLine)
	}

	anchors := AnchorLines(rawLines)
	out := make([]AnchoredLine, 0, endLine-startLine+1)
	for i := startLine; i <= endLine; i++ {
		out = append(out, AnchoredLine{
			Number: i,
			Anchor: anchors[i-1],
			Text:   rawLines[i-1],
		})
	}

	return &ReadResult{
		Path:       displayPath,
		Revision:   rev,
		SHA256:     hashHex,
		TotalLines: total,
		StartLine:  startLine,
		EndLine:    endLine,
		Truncated:  truncated,
		Lines:      out,
	}, nil
}

// Record returns the latest tracked FileRecord for path, if any.
func (t *Tracker) Record(path string) (FileRecord, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, ok := t.files[filepath.ToSlash(path)]
	if !ok {
		return FileRecord{}, false
	}
	return *rec, true
}

// WorkspaceRevision returns the current monotonic workspace revision counter.
func (t *Tracker) WorkspaceRevision() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.nextRev
}

// CheckExternalMutations stats every tracked file (mtime + size), re-hashes any
// changed file (or marks deleted files), bumps their revision, and returns all
// files whose content changed.
func (t *Tracker) CheckExternalMutations() []FileRecord {
	t.mu.Lock()
	defer t.mu.Unlock()

	var changed []FileRecord
	for _, rec := range t.files {
		absPath := rec.AbsPath
		if absPath == "" {
			absPath = filepath.Join(t.repoRoot, filepath.FromSlash(rec.Path))
		}
		info, err := os.Stat(absPath)
		if err != nil {
			if os.IsNotExist(err) && rec.SHA256 != "deleted" {
				t.nextRev++
				rec.SHA256 = "deleted"
				rec.Revision = t.nextRev
				rec.Size = 0
				rec.ModTime = time.Time{}
				changed = append(changed, *rec)
			}
			continue
		}
		// Fast path: unchanged size & mtime, but still hash if mtime is zero or differs.
		if !rec.ModTime.IsZero() && info.Size() == rec.Size && info.ModTime().Equal(rec.ModTime) {
			// Guard against coarse filesystem mtime granularity within the same tick by
			// hashing only when size or mtime changed OR when mtime is within 2s of now.
			if time.Since(info.ModTime()) > 2*time.Second {
				continue
			}
		}
		data, rErr := os.ReadFile(absPath)
		if rErr != nil {
			continue
		}
		newHash := sha256Hex(data)
		rec.Size = info.Size()
		rec.ModTime = info.ModTime()
		if newHash != rec.SHA256 {
			t.nextRev++
			rec.SHA256 = newHash
			rec.Revision = t.nextRev
			changed = append(changed, *rec)
		}
	}
	return changed
}

// SnapshotRecords returns a copy of all tracked file records.
func (t *Tracker) SnapshotRecords() []FileRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]FileRecord, 0, len(t.files))
	for _, rec := range t.files {
		out = append(out, *rec)
	}
	return out
}

func (t *Tracker) recordHash(displayPath, hashHex string) int {
	absPath := filepath.Join(t.repoRoot, filepath.FromSlash(displayPath))
	return t.recordHashWithPath(displayPath, absPath, hashHex)
}

func (t *Tracker) recordHashWithPath(displayPath, absPath, hashHex string) int {
	var size int64
	var modTime time.Time
	if info, err := os.Stat(absPath); err == nil {
		size = info.Size()
		modTime = info.ModTime()
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if rec, ok := t.files[displayPath]; ok {
		rec.AbsPath = absPath
		rec.Size = size
		rec.ModTime = modTime
		if rec.SHA256 == hashHex {
			return rec.Revision
		}
		t.nextRev++
		rec.SHA256 = hashHex
		rec.Revision = t.nextRev
		return rec.Revision
	}
	t.nextRev++
	t.files[displayPath] = &FileRecord{
		Path:     displayPath,
		AbsPath:  absPath,
		SHA256:   hashHex,
		Revision: t.nextRev,
		Size:     size,
		ModTime:  modTime,
	}
	return t.nextRev
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.HasSuffix(s, "\n") {
		s = s[:len(s)-1]
	}
	return strings.Split(s, "\n")
}
