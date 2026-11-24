package edit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CheckpointEntry records the pre-session state of a single file touched by pu.
type CheckpointEntry struct {
	Path           string      `json:"path"`
	Existed        bool        `json:"existed"`
	BackupFile     string      `json:"backup_file,omitempty"`
	Mode           os.FileMode `json:"mode"`
	OriginalSHA256 string      `json:"original_sha256,omitempty"`
}

// CheckpointManifest is persisted at .pu/checkpoints/<session>/manifest.json.
type CheckpointManifest struct {
	SessionID string                     `json:"session_id"`
	CreatedAt time.Time                  `json:"created_at"`
	Entries   map[string]CheckpointEntry `json:"entries"`
}

// Checkpointer snapshots each file before pu's first write to it in a session.
type Checkpointer struct {
	mu        sync.Mutex
	repoRoot  string
	sessionID string
	manifest  *CheckpointManifest
}

// NewCheckpointer creates a session checkpointer rooted at repoRoot.
func NewCheckpointer(repoRoot, sessionID string) *Checkpointer {
	absRoot, err := filepath.Abs(repoRoot)
	if err == nil {
		repoRoot = absRoot
	}
	return &Checkpointer{
		repoRoot:  repoRoot,
		sessionID: sessionID,
		manifest: &CheckpointManifest{
			SessionID: sessionID,
			CreatedAt: time.Now().UTC(),
			Entries:   make(map[string]CheckpointEntry),
		},
	}
}

// SessionID returns the checkpointer's session identifier.
func (c *Checkpointer) SessionID() string {
	return c.sessionID
}

// ChangedFiles returns a sorted list of repo-relative paths touched in this session.
func (c *Checkpointer) ChangedFiles() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.manifest.Entries))
	for p := range c.manifest.Entries {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func checkpointsRoot(repoRoot string) string {
	return filepath.Join(repoRoot, ".pu", "checkpoints")
}

func sessionCheckpointDir(repoRoot, sessionID string) string {
	return filepath.Join(checkpointsRoot(repoRoot), sessionID)
}

// BeforeWrite snapshots relPath if this is the first time the session touches it.
func (c *Checkpointer) BeforeWrite(relPath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	norm := filepath.ToSlash(filepath.Clean(relPath))
	if _, already := c.manifest.Entries[norm]; already {
		return nil
	}

	absPath := filepath.Join(c.repoRoot, filepath.FromSlash(norm))
	sessDir := sessionCheckpointDir(c.repoRoot, c.sessionID)
	filesDir := filepath.Join(sessDir, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return fmt.Errorf("checkpoint mkdir: %w", err)
	}

	entry := CheckpointEntry{
		Path: norm,
	}

	info, err := os.Stat(absPath)
	if err == nil {
		data, rErr := os.ReadFile(absPath)
		if rErr != nil {
			return fmt.Errorf("checkpoint read %s: %w", norm, rErr)
		}
		pathSum := sha256.Sum256([]byte(norm))
		bakName := hex.EncodeToString(pathSum[:16]) + ".bak"
		if wErr := os.WriteFile(filepath.Join(filesDir, bakName), data, 0o644); wErr != nil {
			return fmt.Errorf("checkpoint backup %s: %w", norm, wErr)
		}
		dataSum := sha256.Sum256(data)
		entry.Existed = true
		entry.BackupFile = bakName
		entry.Mode = info.Mode().Perm()
		entry.OriginalSHA256 = hex.EncodeToString(dataSum[:])
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checkpoint stat %s: %w", norm, err)
	}

	c.manifest.Entries[norm] = entry

	if err := c.persistLocked(sessDir); err != nil {
		return err
	}
	return nil
}

func (c *Checkpointer) persistLocked(sessDir string) error {
	data, err := json.MarshalIndent(c.manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint manifest: %w", err)
	}
	manifestPath := filepath.Join(sessDir, "manifest.json")
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		return fmt.Errorf("write checkpoint manifest: %w", err)
	}
	latestPath := filepath.Join(checkpointsRoot(c.repoRoot), "latest")
	if err := os.WriteFile(latestPath, []byte(c.sessionID+"\n"), 0o644); err != nil {
		return fmt.Errorf("write latest session pointer: %w", err)
	}
	return nil
}

// ResolveLatestSession returns the most recent checkpointed session ID in repoRoot.
func ResolveLatestSession(repoRoot string) (string, error) {
	latestPath := filepath.Join(checkpointsRoot(repoRoot), "latest")
	data, err := os.ReadFile(latestPath)
	if err != nil {
		return "", fmt.Errorf("no checkpointed sessions found (%w)", err)
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", errors.New("latest checkpoint pointer is empty")
	}
	return id, nil
}

// LoadManifest reads the checkpoint manifest for sessionID (or latest if empty).
func LoadManifest(repoRoot, sessionID string) (*CheckpointManifest, error) {
	if sessionID == "" {
		var err error
		sessionID, err = ResolveLatestSession(repoRoot)
		if err != nil {
			return nil, err
		}
	}
	manifestPath := filepath.Join(sessionCheckpointDir(repoRoot, sessionID), "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read checkpoint manifest for %s: %w", sessionID, err)
	}
	var m CheckpointManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse checkpoint manifest for %s: %w", sessionID, err)
	}
	return &m, nil
}

// UndoSession restores all files modified/deleted in sessionID to their exact
// pre-session bytes and removes files created in sessionID. Untouched files
// (including the user's own dirty uncommitted files) are never touched.
func UndoSession(repoRoot, sessionID string) ([]string, error) {
	absRoot, err := filepath.Abs(repoRoot)
	if err == nil {
		repoRoot = absRoot
	}
	m, err := LoadManifest(repoRoot, sessionID)
	if err != nil {
		return nil, err
	}
	sessDir := sessionCheckpointDir(repoRoot, m.SessionID)

	paths := make([]string, 0, len(m.Entries))
	for p := range m.Entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	actions := make([]string, 0, len(paths))
	for _, p := range paths {
		entry := m.Entries[p]
		absPath := filepath.Join(repoRoot, filepath.FromSlash(p))
		if entry.Existed {
			bakBytes, err := os.ReadFile(filepath.Join(sessDir, "files", entry.BackupFile))
			if err != nil {
				return actions, fmt.Errorf("read backup for %s: %w", p, err)
			}
			if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
				return actions, fmt.Errorf("restore dir for %s: %w", p, err)
			}
			mode := entry.Mode
			if mode == 0 {
				mode = 0o644
			}
			if err := os.WriteFile(absPath, bakBytes, mode); err != nil {
				return actions, fmt.Errorf("restore %s: %w", p, err)
			}
			actions = append(actions, fmt.Sprintf("restored %s", p))
		} else {
			if err := os.Remove(absPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return actions, fmt.Errorf("remove created file %s: %w", p, err)
			}
			actions = append(actions, fmt.Sprintf("removed %s", p))
		}
	}
	return actions, nil
}

// DiffSession returns a human-readable summary of all changes made during sessionID
// compared against the pre-session checkpoint baseline.
func DiffSession(repoRoot, sessionID string) (string, error) {
	absRoot, err := filepath.Abs(repoRoot)
	if err == nil {
		repoRoot = absRoot
	}
	m, err := LoadManifest(repoRoot, sessionID)
	if err != nil {
		return "", err
	}
	sessDir := sessionCheckpointDir(repoRoot, m.SessionID)

	paths := make([]string, 0, len(m.Entries))
	for p := range m.Entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var sb strings.Builder
	fmt.Fprintf(&sb, "session: %s (%d touched file(s))\n", m.SessionID, len(paths))

	for _, p := range paths {
		entry := m.Entries[p]
		absPath := filepath.Join(repoRoot, filepath.FromSlash(p))

		var oldBytes []byte
		if entry.Existed {
			oldBytes, _ = os.ReadFile(filepath.Join(sessDir, "files", entry.BackupFile))
		}
		newBytes, statErr := os.ReadFile(absPath)
		existsNow := statErr == nil

		switch {
		case !entry.Existed && existsNow:
			newLines := splitLines(string(newBytes))
			fmt.Fprintf(&sb, "A %s (+%d lines)\n", p, len(newLines))
		case entry.Existed && !existsNow:
			oldLines := splitLines(string(oldBytes))
			fmt.Fprintf(&sb, "D %s (-%d lines)\n", p, len(oldLines))
		case !entry.Existed && !existsNow:
			fmt.Fprintf(&sb, "  %s (created then deleted)\n", p)
		default:
			if string(oldBytes) == string(newBytes) {
				fmt.Fprintf(&sb, "  %s (unchanged)\n", p)
			} else {
				oldLines := splitLines(string(oldBytes))
				newLines := splitLines(string(newBytes))
				fmt.Fprintf(&sb, "M %s (%d -> %d lines)\n", p, len(oldLines), len(newLines))
			}
		}
	}
	return sb.String(), nil
}
