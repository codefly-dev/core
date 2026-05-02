package base

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/sandbox"
	"github.com/codefly-dev/core/wool"
)

type NativeEnvironment struct {
	dir string

	// mu guards envs — matches DockerEnvironment's locking. WithEnvironmentVariables
	// can race with concurrent NewProcess otherwise.
	mu   sync.Mutex
	envs []*resources.EnvironmentVariable

	// sb is the OS-level confinement applied to every spawned cmd.
	// nil means no sandboxing (the legacy default while we migrate
	// callers). Set via WithSandbox; once set, every NewProcess'd
	// child runs inside the wrap.
	sb sandbox.Sandbox

	out io.Writer

	ctx context.Context
}

var _ RunnerEnvironment = &NativeEnvironment{}

// NewNativeEnvironment creates a new native runner.
// It runs processes directly on the host using whatever is in PATH.
func NewNativeEnvironment(ctx context.Context, dir string) (*NativeEnvironment, error) {
	w := wool.Get(ctx).In("NewNativeEnvironment")
	env := &NativeEnvironment{
		out: w,
		dir: dir,
	}
	return env, nil
}

func (native *NativeEnvironment) Init(ctx context.Context) error {
	native.ctx = ctx
	return nil
}

func (native *NativeEnvironment) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	w := wool.Get(ctx).In("WithEnvironmentVariables")
	w.Trace("adding environment variables", wool.Field("count", len(envs)))
	native.mu.Lock()
	defer native.mu.Unlock()
	native.envs = append(native.envs, envs...)
}

// WithSandbox attaches a sandbox.Sandbox to this environment. Every
// process spawned via NewProcess will have its underlying exec.Cmd
// wrapped with sb before exec, applying the configured filesystem and
// network confinement.
//
// Calling with sandbox.NewNative() is a no-op pass-through — useful in
// tests and as the default until callers are migrated. Calling with
// nil clears any previously set sandbox.
//
// The same sandbox is shared by every Proc spawned from this env; the
// sandbox itself is single-use per Wrap call but its Wrap method is
// safe to call from multiple Procs because it only mutates the cmd
// it's handed.
func (native *NativeEnvironment) WithSandbox(sb sandbox.Sandbox) *NativeEnvironment {
	native.mu.Lock()
	defer native.mu.Unlock()
	native.sb = sb
	return native
}

func (native *NativeEnvironment) WithBinary(bin string) error {
	p, err := exec.LookPath(bin)
	if err != nil {
		return err
	}
	native.mu.Lock()
	defer native.mu.Unlock()
	// Get the PATH environment variable
	for _, env := range native.envs {
		if env.Key == "PATH" {
			env.Value = fmt.Sprintf("%s:%s", env.Value, filepath.Dir(p))
			return nil
		}
	}
	native.envs = append(native.envs, &resources.EnvironmentVariable{Key: "PATH", Value: filepath.Dir(p)})
	return nil
}

func (native *NativeEnvironment) Shutdown(context.Context) error {
	return nil
}

/*
Proc
*/

type NativeProc struct {
	env    *NativeEnvironment
	output io.Writer
	cmd    []string
	exec   *exec.Cmd
	envs   []*resources.EnvironmentVariable

	stopped  chan interface{}
	stopOnce sync.Once

	// exitCh is closed once exec.Wait returns; exitErr holds the result.
	// Wait() drains this so multiple supervisors can observe the death.
	exitCh   chan struct{}
	exitErr  error
	waitOnce sync.Once

	// optional override
	dir    string
	waitOn string

	// Pipe support for interactive/bidirectional communication.
	stdinReader  *io.PipeReader
	stdinWriter  *io.PipeWriter
	stdoutReader *io.PipeReader
	stdoutWriter *io.PipeWriter
}

func (proc *NativeProc) WithEnvironmentVariablesAppend(ctx context.Context, added *resources.EnvironmentVariable, sep string) {
	for _, env := range proc.envs {
		if env.Key == added.Key {
			env.Value = fmt.Sprintf("%v%s%v", env.Value, sep, added.Value)
			return
		}
	}
	proc.envs = append(proc.envs, added)
}

// IsRunning checks if the exec is still running
func (proc *NativeProc) IsRunning(ctx context.Context) (bool, error) {
	w := wool.Get(ctx).In("NativeProc.IsRunning")
	// Trust the explicit-stop signal over the PID probe. After Stop()
	// reaps the zombie, the kernel can reuse the PID for an
	// unrelated process; ps -p <pid> would then falsely report
	// "running". Same race as NixProc.IsRunning.
	select {
	case <-proc.stopped:
		return false, nil
	default:
	}
	// Check the PID
	if proc.exec == nil || proc.exec.Process == nil {
		return false, nil
	}
	pid := proc.exec.Process.Pid
	w.Trace("checking if process is running", wool.Field("pid", pid))
	// #nosec G204
	cmd := exec.Command("ps", "-p", fmt.Sprintf("%d", pid))
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(err.Error(), "exit") {
			return false, nil
		}
		w.Trace("error checking if process is running", wool.Field("error", err), wool.Field("output", string(output)))
		return false, err
	}
	w.Trace("process is running", wool.Field("output", string(output)))
	if strings.Contains(string(output), fmt.Sprintf("%d", pid)) &&
		!strings.Contains(string(output), "defunct") {
		return true, nil
	}
	return false, nil
}

func (proc *NativeProc) WaitOn(bin string) {
	proc.waitOn = bin
}

func (proc *NativeProc) WithDir(dir string) {
	proc.dir = dir
}

func (proc *NativeProc) WithRunningCmd(_ string) {
}

func (proc *NativeProc) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	w := wool.Get(ctx).In("WithEnvironmentVariables")
	w.Trace("adding environment variables", wool.Field("count", len(envs)))
	proc.envs = append(proc.envs, envs...)
}

func (native *NativeEnvironment) NewProcess(bin string, args ...string) (Proc, error) {
	if _, err := exec.LookPath(bin); err != nil {
		return nil, err
	}
	cmd := append([]string{bin}, args...)
	return &NativeProc{
		env:     native,
		cmd:     cmd,
		output:  native.out,
		stopped: make(chan interface{}),
		exitCh:  make(chan struct{}),
	}, nil
}

func (native *NativeEnvironment) Stop(context.Context) error {
	return nil
}

func (proc *NativeProc) Run(ctx context.Context) error {
	w := wool.Get(ctx).In("NativeProc.Run")
	w.Trace("running process", wool.Field("cmd", proc.cmd))
	err := proc.start(ctx)
	if err != nil {
		return err
	}
	w.Trace("waiting for process to finish or be killed")

	// start() already spawned the single cmd.Wait goroutine that publishes
	// to proc.exitCh. Read from there — never call cmd.Wait twice.
	select {
	case <-proc.exitCh:
		err := proc.exitErr
		if err != nil {
			var exitError *exec.ExitError
			if errors.As(err, &exitError) {
				if strings.Contains(exitError.String(), "signal: terminated") {
					return nil
				}
				return exitError
			} else if strings.Contains(err.Error(), "signal: terminated") {
				return nil
			}
			return w.Wrapf(err, "cannot wait for process")
		}
	case <-proc.stopped:
		w.Trace("process was killed")
	case <-ctx.Done():
		w.Trace("context cancelled, stopping process")
		_ = proc.Stop(ctx)
		return ctx.Err()
	}
	w.Trace("done")
	return nil
}

func (proc *NativeProc) Start(ctx context.Context) error {
	w := wool.Get(ctx).In("NativeProc.Start")
	w.Trace("starting process", wool.Field("cmd", proc.cmd))
	return proc.start(ctx)
}

func (proc *NativeProc) start(ctx context.Context) error {
	w := wool.Get(ctx).In("NativeProc.start", wool.DirField(proc.env.dir))
	// #nosec G204
	cmd := exec.CommandContext(ctx, proc.cmd[0], proc.cmd[1:]...)
	cmd.Dir = proc.env.dir
	if proc.dir != "" {
		cmd.Dir = proc.dir
	}

	// Sandbox wrap, if attached. Done AFTER setting Dir/Env so the
	// inner cmd's working directory carries through (bwrap's --chdir
	// and sandbox-exec both inherit the parent cmd.Dir; we don't
	// re-set it after Wrap rewrites Path/Args).
	//
	// Wrap is the last preparation step before exec — anything set
	// here on cmd after Wrap would land on bwrap/sandbox-exec, not on
	// the actual payload, which is almost always wrong.
	if proc.env.sb != nil {
		if err := proc.env.sb.Wrap(cmd); err != nil {
			return w.Wrapf(err, "sandbox wrap")
		}
	}
	// Create new process group so we can kill all children on stop
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Go 1.20+ ctx-cancel semantics. When ctx is cancelled, send SIGTERM to
	// the pgroup (not just the lead pid) and give it a grace window; if the
	// process is still alive after WaitDelay, the runtime SIGKILLs it AND
	// closes any leaked I/O pipes. Replaces the old hand-rolled timer path
	// in Stop(). Stop() still works — it sends SIGTERM directly — but the
	// ctx-cancel path now cleans up cleanly without a separate goroutine.
	cmd.Cancel = func() error {
		pgid := cmd.Process.Pid
		return syscall.Kill(-pgid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second

	// Start with inherited OS environment (PATH, HOME, etc.)
	cmd.Env = os.Environ()
	// Layer codefly env vars on top (they take precedence). Lock the read —
	// the env may be concurrently appended via WithEnvironmentVariables on
	// another goroutine. Snapshot under the lock, copy out, read lock-free.
	proc.env.mu.Lock()
	envSnapshot := append([]*resources.EnvironmentVariable(nil), proc.env.envs...)
	proc.env.mu.Unlock()
	cmd.Env = append(cmd.Env, resources.EnvironmentVariableAsStrings(envSnapshot)...)
	cmd.Env = append(cmd.Env, resources.EnvironmentVariableAsStrings(proc.envs)...)
	w.Trace("envs", wool.Field("count", len(cmd.Env)))

	// Wire stdin pipe if requested
	if proc.stdinReader != nil {
		cmd.Stdin = proc.stdinReader
	}

	// Wire stdout: raw pipe or forwarded through output
	if proc.stdoutWriter != nil {
		cmd.Stdout = proc.stdoutWriter
		// stderr still goes through the regular output forwarder
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return err
		}
		err = cmd.Start()
		if err != nil {
			return err
		}
		proc.exec = cmd
		go func() {
			defer stderr.Close()
			proc.Forward(ctx, stderr)
		}()
		// Close the stdout pipe writer when the process exits AND publish
		// the exit error for any Wait() callers.
		go func() {
			err := cmd.Wait()
			proc.stdoutWriter.Close()
			proc.publishExit(err)
		}()
	} else {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return err
		}
		err = cmd.Start()
		if err != nil {
			return err
		}
		proc.exec = cmd
		go func() {
			defer stdout.Close()
			proc.Forward(ctx, stdout)
		}()
		go func() {
			defer stderr.Close()
			proc.Forward(ctx, stderr)
		}()
		// Single Wait goroutine — publish the exit so Wait() callers learn.
		go func() {
			err := cmd.Wait()
			proc.publishExit(err)
		}()
	}

	// Persist the pgid so a future `codefly run` can reap this group if the
	// current CLI dies ungracefully (SIGKILL, parent terminal force-closed).
	// Best-effort: a failed write just means this particular proc won't be
	// covered by the startup sweep — the graceful-path tree-kill in Stop
	// still works.
	if perr := writePgidFile(cmd.Process.Pid, cmd.Dir, proc.cmd); perr != nil {
		w.Warn("could not persist pgid file", wool.Field("err", perr))
	}

	w.Trace("done")
	return nil
}

// publishExit records the process exit error and unblocks Wait().
// Called by the single Wait()-on-exec goroutine spawned in start().
func (proc *NativeProc) publishExit(err error) {
	proc.waitOnce.Do(func() {
		proc.exitErr = err
		close(proc.exitCh)
	})
}

// Wait blocks until the process exits or ctx is cancelled. Returns the
// process's exit error (nil on clean exit). Safe to call multiple times.
func (proc *NativeProc) Wait(ctx context.Context) error {
	if proc.exitCh == nil {
		// Process never started — nothing to wait on.
		return nil
	}
	select {
	case <-proc.exitCh:
		return proc.exitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Forward streams r → proc.output one line at a time with newlines intact.
// The previous implementation used strings.TrimSpace which dropped the
// trailing newline between lines, so JSON-lines events merged and the
// log prefix was re-applied per Write call against zero-byte separators.
// Plain io.Copy fixed the newline loss but collapsed all output into a
// single prefix block — worse for interactive tailing. forwardLines
// strikes the middle: scanner gives per-line Write boundaries (so log
// prefixes apply correctly), newline is re-appended so downstream parsers
// see the real separator, and the scanner buffer is sized for 1MiB lines
// so large structured-log events don't silently truncate.
func (proc *NativeProc) Forward(_ context.Context, r io.Reader) {
	forwardLines(r, proc.output)
}

func (proc *NativeProc) WithOutput(output io.Writer) {
	proc.output = output
}

func (proc *NativeProc) StdinPipe() (io.WriteCloser, error) {
	if proc.stdinWriter != nil {
		return nil, fmt.Errorf("StdinPipe already called")
	}
	proc.stdinReader, proc.stdinWriter = io.Pipe()
	return proc.stdinWriter, nil
}

func (proc *NativeProc) StdoutPipe() (io.ReadCloser, error) {
	if proc.stdoutReader != nil {
		return nil, fmt.Errorf("StdoutPipe already called")
	}
	proc.stdoutReader, proc.stdoutWriter = io.Pipe()
	return proc.stdoutReader, nil
}

func (proc *NativeProc) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("NativeProc.Stop")
	w.Trace("stopping process")

	if proc.exec == nil || proc.exec.Process == nil {
		w.Trace("process not started, nothing to stop")
		return nil
	}

	// Kill the entire process group (negative PID) so child processes also die.
	pgid := proc.exec.Process.Pid
	w.Trace("sending SIGTERM to process group", wool.Field("pgid", pgid))
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	// Block until the cmd.Wait goroutine publishes exit, or until the SIGTERM
	// grace window elapses. Returning before the process is reaped is what
	// caused the orphan-leak: callers (Flow.Stop) move on, the CLI exits,
	// and the child outlives the parent. Wait on exitCh — that channel is
	// closed by the single Wait()-on-exec goroutine spawned in start(), so
	// it's the authoritative "process is dead" signal.
	const sigtermGrace = 5 * time.Second
	const sigkillGrace = 2 * time.Second
	select {
	case <-proc.exitCh:
		w.Trace("process exited after SIGTERM")
	case <-time.After(sigtermGrace):
		w.Trace("process did not exit after SIGTERM, sending SIGKILL", wool.Field("pgid", pgid))
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		select {
		case <-proc.exitCh:
			w.Trace("process exited after SIGKILL")
		case <-time.After(sigkillGrace):
			w.Warn("process did not exit even after SIGKILL — leaking", wool.Field("pgid", pgid))
		}
	}

	// Process is confirmed dead — drop the pgid tracking file so the next
	// `codefly run` sweep has nothing to reap for this proc.
	if perr := removePgidFile(pgid); perr != nil {
		w.Trace("could not remove pgid file", wool.Field("err", perr))
	}

	// Signal Run() to bail. close-instead-of-send avoids the previous
	// goroutine leak: the old `go func() { proc.stopped <- struct{}{} }()`
	// blocked forever if Run had already exited via the `done` path or
	// if Stop was called twice. Use sync.Once to make double-close safe.
	proc.stopOnce.Do(func() { close(proc.stopped) })
	return nil
}
