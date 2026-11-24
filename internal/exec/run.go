package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	MaxOutputBytes  = 64 * 1024 // 64 KiB cap per CLAUDE.md M4
	DefaultTimeout  = 60 * time.Second
	sigkillGraceDur = 300 * time.Millisecond
)

// RunOptions configures a shell command execution.
type RunOptions struct {
	Command string
	Dir     string
	Timeout time.Duration
	// Environ overrides os.Environ() before scrubbing (mainly for testing).
	Environ []string
}

// RunResult holds the outcome of a command execution.
type RunResult struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Output     string `json:"output"`
	Truncated  bool   `json:"truncated,omitempty"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Canceled   bool   `json:"canceled,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// Format renders RunResult concisely for the model.
func (r *RunResult) Format() string {
	var sb bytes.Buffer
	fmt.Fprintf(&sb, "exit_code: %d duration_ms: %d", r.ExitCode, r.DurationMS)
	if r.TimedOut {
		sb.WriteString(" (timed_out)")
	}
	if r.Canceled {
		sb.WriteString(" (canceled)")
	}
	if r.Truncated {
		sb.WriteString(" (output_capped_64KiB)")
	}
	sb.WriteByte('\n')
	if r.Output != "" {
		sb.WriteString(r.Output)
		if r.Output[len(r.Output)-1] != '\n' {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// Run executes opts.Command via /bin/sh -c using pipes (no PTY in P0), a new
// process group (Setpgid), scrubbed allowlist environment, 64 KiB output cap,
// and clean process-group teardown (SIGTERM -> 300ms -> SIGKILL) on cancel/timeout.
func Run(ctx context.Context, opts RunOptions) (*RunResult, error) {
	if opts.Command == "" {
		return nil, errors.New("exec: command is required")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	baseEnv := opts.Environ
	if baseEnv == nil {
		baseEnv = os.Environ()
	}

	cmd := osexec.Command("/bin/sh", "-c", opts.Command)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	cmd.Env = ScrubEnv(baseEnv)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	capBuf := &cappedBuffer{max: MaxOutputBytes}
	cmd.Stdout = capBuf
	cmd.Stderr = capBuf

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("exec start: %w", err)
	}

	pid := cmd.Process.Pid
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	var timedOut, canceled bool

	select {
	case waitErr = <-waitCh:
	case <-runCtx.Done():
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			timedOut = true
		} else {
			canceled = true
		}
		// Send SIGTERM to the entire process group, wait 300ms, then SIGKILL.
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		select {
		case waitErr = <-waitCh:
		case <-time.After(sigkillGraceDur):
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			waitErr = <-waitCh
		}
	}

	exitCode := 0
	if waitErr != nil {
		var exitErr *osexec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	if timedOut || canceled {
		if exitCode == 0 {
			exitCode = -1
		}
	}

	return &RunResult{
		Command:    opts.Command,
		ExitCode:   exitCode,
		Output:     capBuf.String(),
		Truncated:  capBuf.Truncated(),
		TimedOut:   timedOut,
		Canceled:   canceled,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

type cappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rem := c.max - c.buf.Len()
	if rem > 0 {
		if len(p) <= rem {
			_, _ = c.buf.Write(p)
		} else {
			_, _ = c.buf.Write(p[:rem])
			c.truncated = true
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	// Always report len(p) written so the child process doesn't block or fail on EPIPE.
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func (c *cappedBuffer) Truncated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.truncated
}
