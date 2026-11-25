package edit

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IsTestFile reports whether a repo-relative path looks like a test file.
func IsTestFile(p string) bool {
	p = filepath.ToSlash(p)
	base := filepath.Base(p)
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return true
	case strings.HasSuffix(p, ".rs") && (strings.HasPrefix(p, "tests/") || strings.Contains(p, "/tests/")):
		return true
	case strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"), strings.HasSuffix(base, "_test.py"):
		return true
	case strings.Contains(base, ".test.") || strings.Contains(base, ".spec."):
		return true
	}
	return false
}

// ChangedTestFiles returns session-touched paths that are test files.
func (c *Checkpointer) ChangedTestFiles() []string {
	var out []string
	for _, p := range c.ChangedFiles() {
		if IsTestFile(p) {
			out = append(out, p)
		}
	}
	return out
}

// BaselineWorktree builds a temporary git worktree that reproduces the current
// workspace except that every non-test file pu changed is put back to its
// pre-session bytes (created non-test files are removed). Test files keep their
// current content. It returns the directory and a cleanup func. Requires git.
func (c *Checkpointer) BaselineWorktree() (string, func(), error) {
	c.mu.Lock()
	entries := make(map[string]CheckpointEntry, len(c.manifest.Entries))
	for k, v := range c.manifest.Entries {
		entries[k] = v
	}
	c.mu.Unlock()

	git := func(dir string, stdin []byte, args ...string) ([]byte, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		return cmd.Output()
	}
	if _, err := git(c.repoRoot, nil, "rev-parse", "--verify", "HEAD"); err != nil {
		return "", nil, fmt.Errorf("baseline needs a git repo: %w", err)
	}
	tmp, err := os.MkdirTemp("", "pu-baseline-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_, _ = git(c.repoRoot, nil, "worktree", "remove", "--force", tmp)
		_ = os.RemoveAll(tmp)
	}
	if _, err := git(c.repoRoot, nil, "worktree", "add", "--detach", "--quiet", tmp, "HEAD"); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("git worktree add: %w", err)
	}
	// Mirror tracked working-tree changes.
	diff, err := git(c.repoRoot, nil, "diff", "HEAD", "--binary")
	if err == nil && len(diff) > 0 {
		if _, err := git(tmp, diff, "apply", "--whitespace=nowarn"); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("git apply: %w", err)
		}
	}
	// Mirror untracked (non-ignored) files.
	untracked, _ := git(c.repoRoot, nil, "ls-files", "--others", "--exclude-standard", "-z")
	for _, p := range strings.Split(string(untracked), "\x00") {
		if p == "" || strings.HasPrefix(p, ".pu/") {
			continue
		}
		data, rErr := os.ReadFile(filepath.Join(c.repoRoot, p))
		if rErr != nil {
			continue
		}
		dst := filepath.Join(tmp, p)
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		_ = os.WriteFile(dst, data, 0o644)
	}
	// Revert pu's non-test changes to their pre-session state.
	sessDir := sessionCheckpointDir(c.repoRoot, c.sessionID)
	for p, e := range entries {
		if IsTestFile(p) {
			continue
		}
		dst := filepath.Join(tmp, filepath.FromSlash(p))
		if !e.Existed {
			_ = os.Remove(dst)
			continue
		}
		data, rErr := os.ReadFile(filepath.Join(sessDir, "files", e.BackupFile))
		if rErr != nil {
			cleanup()
			return "", nil, fmt.Errorf("read backup %s: %w", p, rErr)
		}
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		mode := e.Mode
		if mode == 0 {
			mode = 0o644
		}
		if wErr := os.WriteFile(dst, data, mode); wErr != nil {
			cleanup()
			return "", nil, wErr
		}
	}
	return tmp, cleanup, nil
}
