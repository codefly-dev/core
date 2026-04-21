package code

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// ARCHITECTURE: ShellExec is the sanctioned path for running shell commands
// against a workspace. Mind (the brain) never calls os/exec directly —
// it always goes through an agent's Code.ShellExec RPC. The agent IS the
// plugin boundary; running processes here is explicitly allowed because
// the agent IS the hands.
//
// This implementation lives on DefaultCodeServer so every language agent
// inherits it automatically. It:
//
//  1. Runs the command in its own process group (Setpgid) so stuck
//     children can be killed as a group on timeout.
//  2. Captures stdout and stderr into bounded buffers (2 MiB each) to
//     prevent a noisy command from blowing out the agent's memory.
//  3. Enforces the request's timeout_seconds, or a 30s default, with
//     an explicit process-group kill (SIGTERM → SIGKILL) on expiry.
//  4. Reports timed_out separately from exit_code so callers can tell
//     the two apart.
//
// Security: the workspace's source directory is the root. work_dir in
// the request is resolved relative to source dir, and path traversal
// outside is rejected. Only the environment slice supplied by the
// caller is added to the agent's existing environment — we do not
// read arbitrary files to construct the env.

const (
	shellExecDefaultTimeout = 30 * time.Second
	shellExecMaxTimeout     = 10 * time.Minute
	shellExecMaxOutputBytes = 2 << 20 // 2 MiB per stream
)

// shellExec runs a single command inside the agent's sandbox and returns
// a ShellExecResponse. Errors are always encoded in the response's
// `error` field — the returned Go error is reserved for impossible-to-
// represent failures (never hit in practice).
func (s *DefaultCodeServer) shellExec(ctx context.Context, req *codev0.ShellExecRequest) (*codev0.CodeResponse, error) {
	resp := &codev0.ShellExecResponse{}

	if req == nil {
		resp.ExitCode = -1
		resp.Error = "shell_exec: nil request"
		return wrapShellExec(resp), nil
	}

	// Resolve working directory relative to source root, with traversal check.
	workDir, err := s.resolveShellWorkDir(req.WorkDir)
	if err != nil {
		resp.ExitCode = -1
		resp.Error = err.Error()
		return wrapShellExec(resp), nil
	}

	// Build the command. Two modes: shell line (Command) or argv (Args).
	cmd, err := buildShellCommand(ctx, req)
	if err != nil {
		resp.ExitCode = -1
		resp.Error = err.Error()
		return wrapShellExec(resp), nil
	}
	cmd.Dir = workDir

	// Merge environment: existing + caller-supplied KEY=VALUE additions.
	if len(req.Env) > 0 {
		env := append([]string{}, os.Environ()...)
		env = append(env, req.Env...)
		cmd.Env = env
	}

	// Bounded output capture. Without this cap, a runaway command could
	// allocate gigabytes before the timeout fires.
	var stdoutBuf, stderrBuf boundedBuffer
	stdoutBuf.limit = shellExecMaxOutputBytes
	stderrBuf.limit = shellExecMaxOutputBytes
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// Put the child in its own process group so we can kill the whole
	// tree on timeout via syscall.Kill(-pgid, ...).
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	if err := cmd.Start(); err != nil {
		resp.ExitCode = -1
		resp.Error = fmt.Sprintf("shell_exec: cannot start process: %v", err)
		return wrapShellExec(resp), nil
	}

	// Enforce timeout with an explicit goroutine rather than relying on
	// ctx.Done() cascading to CommandContext — we want the process GROUP
	// killed, not just the direct child.
	timeout := shellExecTimeout(req.TimeoutSeconds)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		resp.ExitCode = int32(extractExitCode(err))
	case <-time.After(timeout):
		resp.TimedOut = true
		pgid := cmd.Process.Pid
		// SIGTERM to the group, wait briefly, then SIGKILL if still alive.
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		select {
		case err := <-done:
			resp.ExitCode = int32(extractExitCode(err))
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			<-done // reap
			resp.ExitCode = -1
		}
	case <-ctx.Done():
		// Parent context cancelled — kill the group and return.
		pgid := cmd.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done // reap
		resp.ExitCode = -1
		resp.Error = fmt.Sprintf("shell_exec: context cancelled: %v", ctx.Err())
	}

	resp.Stdout = stdoutBuf.String()
	resp.Stderr = stderrBuf.String()
	return wrapShellExec(resp), nil
}

// buildShellCommand builds an *exec.Cmd from the request, using args-mode
// when req.Args is set and shell-mode when req.Command is set.
func buildShellCommand(ctx context.Context, req *codev0.ShellExecRequest) (*exec.Cmd, error) {
	if len(req.Args) > 0 {
		if req.Args[0] == "" {
			return nil, fmt.Errorf("shell_exec: args[0] is empty")
		}
		return exec.CommandContext(ctx, req.Args[0], req.Args[1:]...), nil
	}
	if req.Command == "" {
		return nil, fmt.Errorf("shell_exec: either command or args must be set")
	}
	return exec.CommandContext(ctx, "sh", "-c", req.Command), nil
}

// resolveShellWorkDir resolves the optional working directory against
// the server's source root. Returns the source root if empty. Rejects
// attempts to escape the source tree (path traversal).
func (s *DefaultCodeServer) resolveShellWorkDir(requested string) (string, error) {
	root := filepath.Clean(s.SourceDir)
	if requested == "" {
		return root, nil
	}
	cleaned := filepath.Clean(requested)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("shell_exec: absolute work_dir not allowed: %q", requested)
	}
	abs := filepath.Join(root, cleaned)
	// Guard: the resolved abs must still be under the root.
	if !isWithin(abs, root) {
		return "", fmt.Errorf("shell_exec: work_dir escapes source root: %q", requested)
	}
	return abs, nil
}

// isWithin returns true if path is equal to root or a descendant of it.
func isWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if len(rel) >= 2 && rel[:2] == ".." {
		return false
	}
	return true
}

// shellExecTimeout returns the effective timeout, applying defaults and
// an upper bound so callers can't ask for unbounded waits.
func shellExecTimeout(requested int32) time.Duration {
	if requested <= 0 {
		return shellExecDefaultTimeout
	}
	d := time.Duration(requested) * time.Second
	if d > shellExecMaxTimeout {
		return shellExecMaxTimeout
	}
	return d
}

// extractExitCode returns the child's exit code, or a sentinel for
// non-exit errors (signals, process killed).
func extractExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

// wrapShellExec wraps a ShellExecResponse in a CodeResponse oneof.
func wrapShellExec(resp *codev0.ShellExecResponse) *codev0.CodeResponse {
	return &codev0.CodeResponse{
		Result: &codev0.CodeResponse_ShellExec{ShellExec: resp},
	}
}

// boundedBuffer is an io.Writer that accepts up to `limit` bytes and
// silently drops the rest. This prevents a chatty command from blowing
// out the agent's memory while still letting the caller see the head
// of the output (the useful part for most failures).
type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len() >= b.limit {
		b.truncated = true
		return len(p), nil // pretend we wrote it so the child doesn't block
	}
	room := b.limit - b.buf.Len()
	if len(p) > room {
		b.buf.Write(p[:room])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *boundedBuffer) String() string {
	s := b.buf.String()
	if b.truncated {
		s += "\n... (truncated)"
	}
	return s
}

// Ensure io.Writer compile-time check.
var _ io.Writer = (*boundedBuffer)(nil)
