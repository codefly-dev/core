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
	"github.com/codefly-dev/core/wool"
)

type NativeEnvironment struct {
	dir string

	// mu guards envs — matches DockerEnvironment's locking. WithEnvironmentVariables
	// can race with concurrent NewProcess otherwise.
	mu   sync.Mutex
	envs []*resources.EnvironmentVariable

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
	group  *TrackedProcessGroup
	envs   []*resources.EnvironmentVariable
	// carriers holds the file-delivered values this process was started
	// with; they are removed once it has exited.
	carriers *processCarriers

	// lifecycleMu serializes Start's cmd.Start/exec publication with Stop and
	// IsRunning. stopRequested prevents a Stop-before-Start race from launching
	// a process after Stop has already returned.
	lifecycleMu   sync.Mutex
	stopRequested bool

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

	// forwarderWG tracks the stdout/stderr forwarder + cmd.Wait
	// goroutines spawned by start(). Run() blocks on this before
	// returning so callers reading proc.output never race against
	// in-flight Forward calls. Without this, fast commands like
	// `echo hello` lose their output on slow CI runners — caught
	// by Linux CI flakes; local macOS was fast enough to mask the
	// race. NixProc had this; NativeProc was missing it.
	forwarderWG sync.WaitGroup
}

// lockedWriter serializes Write() calls. Two forwarder goroutines
// (stdout + stderr) share proc.output; user-supplied writers
// (bytes.Buffer in tests, *Wool in production) usually aren't
// safe for concurrent Write. This wrapper closes the race.
//
// Why we have to do this ourselves: Go's os/exec docs guarantee
// "at-most-one concurrent Write" only when cmd.Stdout/Stderr are
// SET DIRECTLY to the same comparable writer. We use StdoutPipe/
// StderrPipe + manual forwarders (so we can do per-line prefixing
// for wool), which bypasses that guarantee — every byte goes
// through forwardLines first, and the lock has to live on our side.
//
// On macOS, the test's stderr stays empty, only the stdout
// forwarder ever calls Write, no race. On GitHub Actions Ubuntu
// runners, a tty wrapper injects an ANSI Device Status Report
// (\033[6n) on stderr — both forwarders Write concurrently and
// the unsynced bytes.Buffer cursor scrambles or loses writes.
// Fixed under #19804 territory; lockedWriter is the local fix.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (lw *lockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
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
	proc.lifecycleMu.Lock()
	runningCmd := proc.exec
	proc.lifecycleMu.Unlock()
	if runningCmd == nil || runningCmd.Process == nil {
		return false, nil
	}
	pid := runningCmd.Process.Pid
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
	w.Trace("running process", wool.Field("cmd", CommandSummary(proc.cmd)))
	err := proc.start(ctx)
	if err != nil {
		return err
	}
	// Wait for forwarder goroutines to drain BEFORE returning. Without
	// this, callers reading proc.output (or any user-supplied writer)
	// race against the forwarder still copying bytes from the pipe.
	// `echo hello` would land in proc.output occasionally-empty on
	// slow Linux CI runners; local macOS was fast enough to mask the
	// race. NixProc has the same defer; this brings parity.
	defer proc.forwarderWG.Wait()
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
	w.Trace("starting process", wool.Field("cmd", CommandSummary(proc.cmd)))
	return proc.start(ctx)
}

func (proc *NativeProc) start(ctx context.Context) (startErr error) {
	w := wool.Get(ctx).In("NativeProc.start", wool.DirField(proc.env.dir))
	// #nosec G204
	cmd := exec.CommandContext(ctx, proc.cmd[0], proc.cmd[1:]...)
	cmd.Dir = proc.env.dir
	if proc.dir != "" {
		cmd.Dir = proc.dir
	}

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
	carriers, err := prepareProcessCarriers(envSnapshot, proc.envs)
	if err != nil {
		return w.Wrapf(err, "cannot prepare the process environment")
	}
	cmd.Env = append(cmd.Env, carriers.environ...)
	proc.carriers = carriers
	defer func() {
		// A process that never started never exits: release its files now.
		if startErr != nil {
			carriers.release()
		}
	}()
	w.Trace("envs", wool.Field("count", len(cmd.Env)))

	// Wire stdin pipe if requested
	if proc.stdinReader != nil {
		cmd.Stdin = proc.stdinReader
	}

	// Serialize stdout+stderr forwarders' writes onto proc.output.
	// Both forwarders share the same writer; user-supplied writers
	// (bytes.Buffer, *Wool, *strings.Builder) are not safe for
	// concurrent Write. See lockedWriter doc for the Linux-CI
	// flake history that led to this. Idempotent — wrapping a
	// lockedWriter again is harmless.
	if proc.output != nil {
		if _, alreadyLocked := proc.output.(*lockedWriter); !alreadyLocked {
			proc.output = &lockedWriter{w: proc.output}
		}
	}

	// Wire stdout: raw pipe or forwarded through output
	if proc.stdoutWriter != nil {
		cmd.Stdout = proc.stdoutWriter
		// stderr still goes through the regular output forwarder
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return err
		}
		err = proc.startCommand(ctx, cmd)
		if err != nil {
			_ = stderr.Close()
			return err
		}
		proc.forwarderWG.Add(1)
		go func() {
			defer proc.forwarderWG.Done()
			defer stderr.Close()
			proc.Forward(ctx, stderr)
		}()
		// Drain the StderrPipe forwarder to EOF BEFORE reaping. os/exec
		// closes the pipe read-end the instant cmd.Wait sees the process
		// exit, so a concurrent Wait races the forwarder's reads and
		// truncates output. The cmd.Stdout writer path is os/exec-managed,
		// so only the manual StderrPipe needs the ordering. Then close the
		// stdout pipe writer and publish the exit for Wait() callers.
		go func() {
			proc.forwarderWG.Wait()
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
		err = proc.startCommand(ctx, cmd)
		if err != nil {
			_ = stdout.Close()
			_ = stderr.Close()
			return err
		}
		proc.forwarderWG.Add(2)
		go func() {
			defer proc.forwarderWG.Done()
			defer stdout.Close()
			proc.Forward(ctx, stdout)
		}()
		go func() {
			defer proc.forwarderWG.Done()
			defer stderr.Close()
			proc.Forward(ctx, stderr)
		}()
		// CRITICAL ordering: drain both forwarders to EOF BEFORE cmd.Wait.
		// os/exec closes the StdoutPipe/StderrPipe read-ends the moment Wait
		// sees the process exit — the docs are explicit: "it is incorrect to
		// call Wait before all reads from the pipe have completed." Running
		// Wait concurrently with the forwarders races that close against
		// their reads and, on a fast-exiting process, drops the entire
		// output (the "occasionally-empty on slow Linux CI" symptom this
		// runner has chased for a while). Forwarders EOF when the process
		// exits and the kernel closes the write-ends; Stop()/SIGKILL is the
		// escape hatch for a process that refuses to die.
		go func() {
			proc.forwarderWG.Wait()
			proc.publishExit(cmd.Wait())
		}()
	}

	w.Trace("done")
	return nil
}

func (proc *NativeProc) startCommand(_ context.Context, cmd *exec.Cmd) error {
	proc.lifecycleMu.Lock()
	defer proc.lifecycleMu.Unlock()
	if proc.stopRequested {
		return errors.New("process was stopped before it could start")
	}
	if proc.exec != nil {
		return errors.New("process already started")
	}
	group, err := StartTrackedProcessGroup(cmd)
	if err != nil {
		return err
	}
	proc.exec = cmd
	proc.group = group
	return nil
}

// publishExit records the process exit error and unblocks Wait().
// Called by the single Wait()-on-exec goroutine spawned in start().
func (proc *NativeProc) publishExit(err error) {
	proc.waitOnce.Do(func() {
		proc.exitErr = err
		proc.lifecycleMu.Lock()
		cmd := proc.exec
		group := proc.group
		proc.lifecycleMu.Unlock()
		if cmd != nil && cmd.Process != nil && group != nil {
			_ = group.RemoveIfDead()
		}
		proc.carriers.release()
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
// strikes the middle: per-line Write boundaries (so log prefixes apply
// correctly) with newlines intact so downstream parsers see the real
// separator, and no token cap so large structured-log events don't
// silently truncate.
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

	proc.lifecycleMu.Lock()
	proc.stopRequested = true
	cmd := proc.exec
	group := proc.group
	proc.lifecycleMu.Unlock()
	if cmd == nil || cmd.Process == nil {
		w.Trace("process not started, nothing to stop")
		proc.stopOnce.Do(func() { close(proc.stopped) })
		return nil
	}

	err := stopTrackedProcessGroup(ctx, cmd, group, proc.exitCh)

	// Signal Run() to bail. close-instead-of-send avoids the previous
	// goroutine leak: the old `go func() { proc.stopped <- struct{}{} }()`
	// blocked forever if Run had already exited via the `done` path or
	// if Stop was called twice. Use sync.Once to make double-close safe.
	proc.stopOnce.Do(func() { close(proc.stopped) })
	return err
}

// Stop budgets. stopTermGrace is what a service gets to honour SIGTERM — a dev
// server flushing state, a database checkpointing — before the kill phase.
// stopReapGrace bounds the wait for the leader to be reaped once its group is
// empty: the forwarders reach EOF when the last member holding the leader's
// stdout dies, so it only has to outlast scheduling.
const (
	stopTermGrace = 5 * time.Second
	stopReapGrace = 2 * time.Second
)

// stopTrackedProcessGroup is the one teardown NativeProc and NixProc share: end
// the whole process group, verify it is empty, then wait for the leader to be
// reaped and drop the registry record.
//
// The unit is the group, never the leader. Stop used to signal the group once
// and then watch the leader: a leader that exited on SIGTERM ended the wait
// while a descendant that ignored it kept running, and the SIGKILL fallback
// authenticated the group with the reaper's proof, which refused a group whose
// leader was an unreaped zombie and signalled nothing. Either way the tree was
// reparented to init still holding its port, and the next run of the service
// failed to bind. terminateAsOwner escalates on group liveness and
// reports a survivor instead;
// see terminateAsOwner for why the owner authenticates differently.
//
// The teardown ignores the caller's cancellation: a Stop that gives up because
// its caller did is exactly the leak this exists to prevent, and the escalation
// is bounded on its own (stopTermGrace plus the kill sweeps).
func stopTrackedProcessGroup(ctx context.Context, cmd *exec.Cmd, group *TrackedProcessGroup, exited <-chan struct{}) error {
	w := wool.Get(ctx).In("stopTrackedProcessGroup")
	pgid := cmd.Process.Pid
	w.Trace("terminating process group", wool.Field("pgid", pgid))

	var failures []error
	if group != nil {
		if err := group.terminateAsOwner(context.WithoutCancel(ctx), stopTermGrace); err != nil {
			failures = append(failures, fmt.Errorf("terminate process group %d: %w", pgid, err))
		}
	}

	// The group is empty, so every holder of the leader's stdout and stderr is
	// gone and the single cmd.Wait goroutine can reap the leader.
	select {
	case <-exited:
	case <-time.After(stopReapGrace):
		failures = append(failures, fmt.Errorf("process group %d leader was not reaped within %s", pgid, stopReapGrace))
	}

	// Drop the registration only if no descendant remains in the group; a
	// survivor keeps its record so the next start's reaper still finds it.
	if group != nil {
		if err := group.RemoveIfDead(); err != nil {
			w.Trace("could not remove pgid file", wool.Field("err", err))
		}
	}
	if len(failures) > 0 {
		err := errors.Join(failures...)
		w.Warn("process group survived Stop", wool.Field("pgid", pgid), wool.ErrField(err))
		return err
	}
	return nil
}
