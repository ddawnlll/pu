package eval

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
)

func gitIn(dir string, args ...string) (string, error) {
	cmd := osexec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// isolatedCheckout initialises dst as a fresh repo holding only commit and its
// ancestors (fetched by SHA), detached at commit, with no refs, tags or remotes.
func isolatedCheckout(repoDir, commit, dst string) error {
	sha, err := gitIn(repoDir, "rev-parse", "--verify", commit+"^{commit}")
	if err != nil {
		return err
	}
	sha = strings.TrimSpace(sha)
	src, _ := filepath.Abs(repoDir)
	steps := [][]string{
		{"init", "-q"},
		{"-c", "protocol.file.allow=always", "fetch", "-q", "--no-tags", "file://" + src, sha},
		{"checkout", "-q", "--detach", sha},
	}
	for _, s := range steps {
		if _, err := gitIn(dst, s...); err != nil {
			return err
		}
	}
	_ = os.Remove(filepath.Join(dst, ".git", "FETCH_HEAD"))
	return nil
}

// fetchForAcceptance brings the source repo's branch heads into the isolated
// checkout (under a private namespace) after the agent finished, so acceptance
// commands like `git checkout <fixsha> -- tests` work. No-op for plain worktrees.
func fetchForAcceptance(repoDir, wtDir string) {
	if fi, err := os.Stat(filepath.Join(wtDir, ".git")); err != nil || !fi.IsDir() {
		return
	}
	src, _ := filepath.Abs(repoDir)
	if shallow, _ := gitIn(wtDir, "rev-parse", "--is-shallow-repository"); strings.TrimSpace(shallow) == "true" {
		_, _ = gitIn(wtDir, "-c", "protocol.file.allow=always", "fetch", "-q", "--no-tags", "--update-shallow", "file://"+src, "+refs/heads/*:refs/pu-eval-accept/*")
		return
	}
	_, _ = gitIn(wtDir, "-c", "protocol.file.allow=always", "fetch", "-q", "--no-tags", "file://"+src, "+refs/heads/*:refs/pu-eval-accept/*")
}
