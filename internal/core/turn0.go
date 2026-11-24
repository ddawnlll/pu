package core

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LoadProjectRules reads AGENTS.md and/or PU.md from repoRoot once at session
// start in deterministic order (AGENTS.md then PU.md). It is pinned into Layer 3
// of the KV prefix and never mutated mid-session.
func LoadProjectRules(repoRoot string) string {
	var parts []string
	for _, name := range []string{"AGENTS.md", "PU.md"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			continue
		}
		trimmed := strings.TrimSpace(string(data))
		if trimmed == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("# %s\n\n%s", name, trimmed))
	}
	return strings.Join(parts, "\n\n")
}

// BuildTurn0Fingerprint builds the deterministic local repository fingerprint
// injected into the first user turn (never the system prompt).
func BuildTurn0Fingerprint(ctx context.Context, repoRoot string) string {
	var sb strings.Builder
	sb.WriteString("<turn0_fingerprint>\n")

	gitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if branch, err := runGit(gitCtx, repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && branch != "" {
		fmt.Fprintf(&sb, "git_branch: %s\n", branch)
	} else {
		sb.WriteString("git_branch: (not a git repo)\n")
	}

	if status, err := runGit(gitCtx, repoRoot, "status", "--porcelain=v2"); err == nil {
		if status == "" {
			sb.WriteString("git_status: clean\n")
		} else {
			lines := strings.Split(status, "\n")
			const maxStatusLines = 80
			truncated := false
			if len(lines) > maxStatusLines {
				lines = lines[:maxStatusLines]
				truncated = true
			}
			sb.WriteString("git_status_porcelain_v2:\n")
			for _, l := range lines {
				fmt.Fprintf(&sb, "  %s\n", l)
			}
			if truncated {
				sb.WriteString("  ... (truncated)\n")
			}
		}
	}

	// Top-level tree summary
	if entries, err := os.ReadDir(repoRoot); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			n := e.Name()
			if n == ".git" || n == ".pu" {
				continue
			}
			if e.IsDir() {
				n += "/"
			}
			names = append(names, n)
		}
		sort.Strings(names)
		const maxEntries = 100
		if len(names) > maxEntries {
			names = append(names[:maxEntries], "...")
		}
		fmt.Fprintf(&sb, "top_level: %s\n", strings.Join(names, ", "))
	}

	// Manifests & test runner detection
	manifestCandidates := []struct {
		file   string
		runner string
	}{
		{"go.mod", "go test ./..."},
		{"package.json", "npm test"},
		{"Cargo.toml", "cargo test"},
		{"pyproject.toml", "pytest"},
		{"pytest.ini", "pytest"},
		{"Makefile", "make test"},
	}

	var manifests []string
	var runners []string
	seenRunner := make(map[string]bool)
	for _, c := range manifestCandidates {
		if _, err := os.Stat(filepath.Join(repoRoot, c.file)); err == nil {
			manifests = append(manifests, c.file)
			if !seenRunner[c.runner] {
				seenRunner[c.runner] = true
				runners = append(runners, c.runner)
			}
		}
	}
	if len(manifests) > 0 {
		fmt.Fprintf(&sb, "manifests: %s\n", strings.Join(manifests, ", "))
	} else {
		sb.WriteString("manifests: none\n")
	}
	if len(runners) > 0 {
		fmt.Fprintf(&sb, "test_runners: %s\n", strings.Join(runners, ", "))
	} else {
		sb.WriteString("test_runners: unknown\n")
	}

	sb.WriteString("</turn0_fingerprint>")
	return sb.String()
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := osexec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
