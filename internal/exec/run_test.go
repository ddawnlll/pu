package exec

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExecChildEnvContainsNoKeys(t *testing.T) {
	env := append(os.Environ(),
		"OPENCODE_API_KEY=oc-secret-key-xyz",
		"OPENAI_API_KEY=sk-secret-key-xyz",
		"MY_SECRET=super-secret",
		"AUTH_TOKEN=tok-secret",
	)
	res, err := Run(context.Background(), RunOptions{
		Command: "env",
		Environ: env,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, output=%s", res.ExitCode, res.Output)
	}
	for _, forbidden := range []string{"OPENCODE_API_KEY", "OPENAI_API_KEY", "MY_SECRET", "AUTH_TOKEN", "oc-secret-key-xyz", "sk-secret-key-xyz"} {
		if strings.Contains(res.Output, forbidden) {
			t.Fatalf("child env leaked %q:\n%s", forbidden, res.Output)
		}
	}
}

func TestExecCancelLeavesNoOrphanProcesses(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")

	// Spawn a background child process inside the shell that ignores nothing, write its PID, then wait.
	cmd := "sleep 60 & echo $! > " + pidFile + "; wait"

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Wait until pidFile is written, then cancel (simulating SIGINT).
		for i := 0; i < 100; i++ {
			if data, err := os.ReadFile(pidFile); err == nil && len(strings.TrimSpace(string(data))) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()

	res, err := Run(ctx, RunOptions{
		Command: cmd,
		Dir:     dir,
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Canceled {
		t.Fatalf("expected Canceled=true, got %+v", res)
	}

	rawPID, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read child.pid: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if err != nil || childPID <= 0 {
		t.Fatalf("invalid child PID %q: %v", string(rawPID), err)
	}

	// Give the OS a brief moment to reap the killed child process.
	alive := true
	for i := 0; i < 20; i++ {
		if err := syscall.Kill(childPID, 0); err != nil {
			alive = false
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if alive {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		t.Fatalf("orphan child process %d survived process-group cancel", childPID)
	}
}

func TestExecOutputCappedAt64KiB(t *testing.T) {
	// Generate ~128 KiB of output.
	res, err := Run(context.Background(), RunOptions{
		Command: "dd if=/dev/zero bs=1024 count=128 2>/dev/null | tr '\\0' 'A'",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Truncated {
		t.Fatalf("expected Truncated=true")
	}
	if len(res.Output) != MaxOutputBytes {
		t.Fatalf("output len = %d, want %d", len(res.Output), MaxOutputBytes)
	}
}
