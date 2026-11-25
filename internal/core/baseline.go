package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ddawnlll/pu/internal/edit"
	puexec "github.com/ddawnlll/pu/internal/exec"
	"github.com/ddawnlll/pu/internal/plan"
)

var testCmdTokens = []string{
	"go test", "cargo test", "cargo nextest", "pytest", "python -m pytest", "python -m unittest",
	"python3 -m pytest", "python3 -m unittest", "npm test", "pnpm test", "yarn test", "bun test",
	"npx vitest", "npx jest",
}

func isTestCmd(cmd string) bool {
	l := strings.ToLower(cmd)
	for _, t := range testCmdTokens {
		if strings.Contains(l, t) {
			return true
		}
	}
	return false
}

// autoBaseline establishes "failed before" for a passing test command without
// spending a model turn: when pu has added or changed test files AND changed
// non-test code, it reruns the same command in a temporary worktree where the
// non-test changes are reverted to the pre-edit checkpoint (tests kept). If it
// fails there, the passing evidence is marked as covering the change. The rule
// stays as strict as before: covering still means "fails without the change,
// passes with it" on the same command.
func autoBaseline(ctx context.Context, cp *edit.Checkpointer, planMgr *plan.Manager, cmd string, timeout time.Duration, ev plan.Evidence) string {
	if ev.ExitCode != 0 || ev.Covers || !isTestCmd(cmd) || planMgr.SeenRed(cmd) || !needsCoverage(planMgr) {
		return ""
	}
	tests := cp.ChangedTestFiles()
	if len(tests) == 0 || len(tests) == len(cp.ChangedFiles()) {
		return ""
	}
	dir, cleanup, err := cp.BaselineWorktree()
	if err != nil {
		return ""
	}
	defer cleanup()
	res, err := puexec.Run(ctx, puexec.RunOptions{Command: cmd, Dir: dir, Timeout: timeout})
	if err != nil || res.TimedOut {
		return ""
	}
	if res.ExitCode != 0 {
		planMgr.MarkFailedBefore(ev.ID)
		return fmt.Sprintf("[baseline: same command fails with your non-test changes reverted (exit %d); %s covers the change]\n", res.ExitCode, ev.ID)
	}
	return ""
}

// needsCoverage reports whether some plan step marked done still lacks
// covering evidence, i.e. the baseline result could change completion.
func needsCoverage(pm *plan.Manager) bool {
	for _, s := range pm.OpenSteps() {
		if s.Status == "done" {
			return true
		}
	}
	return false
}
