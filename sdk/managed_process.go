package sdk

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/codefly-dev/core/runners/base"
)

// managedProcess wraps an exec.Cmd with a lifecycle that survives the
// rough edges of running long-lived subprocesses from a Go test binary:
//
//  1. The child leads its own process group, started through
//     base.StartOwnedProcessGroup so its identity is pinned before it
//     can fork. Cleanup terminates the whole group; without it, killing
//     the shell/CLI leaves its grandchildren behind as orphans. Docker
//     containers the child created are owned by the Docker daemon and
//     are NOT in this group — no OS signal removes them, that teardown
//     belongs to the CLI.
//
//  2. Stdout and stderr are piped through internal readers that we drain
//     in goroutines and echo to the host process's stdout/stderr. This
//     decouples the child's file descriptors from os.Stdout/os.Stderr so
//     the Go test runner doesn't hang on WaitDelay waiting for those FDs
//     to have no more writers.
//
//  3. A single supervisor goroutine calls cmd.Wait() so the child is
//     always reaped — exiting cleanly, or dying after Kill(). The boolean
//     `exited` flips under the mutex; callers that need to observe it
//     (e.g., tests) can lock mu. Reaping the leader is deliberately kept
//     separate from group teardown: the leader can exit long before the
//     group is empty.
//
//  4. An OS signal trap converts SIGINT / SIGTERM / SIGHUP into a call
//     to Kill() so Ctrl-C during `go test` doesn't orphan the group.
//     The trap is installed once per managedProcess and removed on Kill.
//
// The struct is intentionally independent of the codefly CLI so it can
// be unit-tested with any command (see managed_process_test.go).
type managedProcess struct {
	cmd   *exec.Cmd
	group *base.TrackedProcessGroup

	// stdoutR and stderrR are line-buffered readers over the child's
	// stdout and stderr pipes. Tests read from stdoutR via readLine to
	// capture specific lines. Production callers must call Echo() to
	// start goroutines that drain both readers into os.Stdout / os.Stderr
	// — otherwise the pipes eventually fill and block the child.
	stdoutR *bufio.Reader
	stderrR *bufio.Reader
	echoing bool

	// Teardown state guarded by mu.
	mu         sync.Mutex
	exited     bool
	sigCh      chan os.Signal
	waitedOnce sync.Once
	waitErr    error
	done       chan struct{}

	// Kill state. killOnce runs the teardown exactly once; every caller
	// blocks on killDone and reads the single killErr it produced.
	killOnce sync.Once
	killDone chan struct{}
	killErr  error
}

// killBudget bounds a whole Kill: the SIGTERM grace, the SIGKILL grace and
// the supervisor's release all have to fit inside it. It is a hard stop, not
// a delay — cooperative children are gone in milliseconds.
const killBudget = 15 * time.Second

// startManaged starts cmd in its own process group with the lifecycle
// guarantees described on managedProcess. The returned process is already
// running — the internal supervisor and signal-trap goroutines are live.
//
// Callers are expected to call Kill() (or let the signal trap do it)
// during teardown. It is safe to call Kill multiple times.
func startManaged(_ any, cmd *exec.Cmd) (*managedProcess, error) {
	// We wire our own pipes so we can echo to os.Stdout/Stderr in
	// goroutines we control. Otherwise the child would inherit the
	// parent's stdio FDs and the Go test runner would wait on them.
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("managed: StdoutPipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("managed: StderrPipe: %w", err)
	}

	// Start the child as the leader of its own process group, with its
	// identity captured so cleanup can prove the group is still ours.
	group, err := base.StartOwnedProcessGroup(cmd)
	if err != nil {
		return nil, fmt.Errorf("managed: Start: %w", err)
	}

	mp := &managedProcess{
		cmd:      cmd,
		group:    group,
		stdoutR:  bufio.NewReader(stdoutPipe),
		stderrR:  bufio.NewReader(stderrPipe),
		done:     make(chan struct{}),
		killDone: make(chan struct{}),
	}

	// Supervisor goroutine — reaps the child on exit.
	go mp.supervise()

	// Signal trap — forwards SIGINT/SIGTERM/SIGHUP to Kill() so Ctrl-C
	// during `go test` tears down the whole group.
	mp.sigCh = make(chan os.Signal, 1)
	signal.Notify(mp.sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go mp.watchSignals()

	return mp, nil
}

// Echo starts two goroutines that drain the child's stdout and stderr
// into os.Stdout and os.Stderr respectively. Call this exactly once,
// from production callers, right after startManaged. Tests that want
// to observe specific output should NOT call Echo — they read directly
// via readLine. Calling Echo twice is a no-op.
func (mp *managedProcess) Echo() {
	mp.mu.Lock()
	if mp.echoing {
		mp.mu.Unlock()
		return
	}
	mp.echoing = true
	mp.mu.Unlock()
	go echoPipe(mp.stdoutR, os.Stdout)
	go echoPipe(mp.stderrR, os.Stderr)
}

// supervise blocks in cmd.Wait() so the child is reaped exactly once.
// It's safe to call concurrently with Kill — Wait is goroutine-safe for
// exec.Cmd, and the mutex guards the `exited` flag.
func (mp *managedProcess) supervise() {
	mp.waitedOnce.Do(func() {
		err := mp.cmd.Wait()
		mp.mu.Lock()
		mp.exited = true
		mp.waitErr = err
		mp.mu.Unlock()
		close(mp.done)
	})
}

// Done is closed when the supervised child has exited and been reaped.
func (mp *managedProcess) Done() <-chan struct{} {
	return mp.done
}

// WaitError reports the child's exit result after Done has closed.
func (mp *managedProcess) WaitError() error {
	<-mp.done
	mp.mu.Lock()
	defer mp.mu.Unlock()
	return mp.waitErr
}

// watchSignals blocks until a shutdown signal arrives, then kills the
// process group. Returns when the signal channel is closed (by Kill).
func (mp *managedProcess) watchSignals() {
	sig, ok := <-mp.sigCh
	if !ok {
		return // channel closed — Kill was called cleanly, we're done
	}
	// Re-raise the signal to the default handler after we finish cleanup,
	// so the parent process also dies. Otherwise `go test` would hang on
	// Ctrl-C waiting for THIS goroutine.
	_ = mp.Kill()
	signal.Stop(mp.sigCh)
	// Restore default behavior and re-deliver so the process exits with
	// the expected signal status.
	p, err := os.FindProcess(os.Getpid())
	if err == nil {
		_ = p.Signal(sig)
	}
}

// Kill terminates the entire process group of the managed child and returns
// once the group is gone, the supervisor has been released and the signal
// trap is stopped — or once the kill budget is spent, whichever comes first.
//
// The group, not the leader, is the unit of cleanup. Escalation to SIGKILL is
// driven by group liveness, so a descendant that ignores SIGTERM is killed
// even when its parent honoured SIGTERM and was reaped immediately. Signals go
// to authenticated member incarnations rather than to a bare -pgid, so a pgid
// the kernel recycled once the group emptied is never hit.
//
// Killing an OS process group cannot remove Docker containers the child
// created: they are children of the Docker daemon, not of this group. Removing
// them is the CLI's job, driven over the CLI control channel.
//
// Kill is idempotent. Concurrent and repeated calls all block until the first
// call has finished and return the result it produced.
func (mp *managedProcess) Kill() error {
	mp.killOnce.Do(func() {
		mp.killErr = mp.terminate()
		close(mp.killDone)
	})
	<-mp.killDone
	return mp.killErr
}

func (mp *managedProcess) terminate() error {
	defer mp.stopSignalTrap()
	if mp.group == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), killBudget)
	defer cancel()

	err := mp.group.Terminate(ctx)
	select {
	case <-mp.done:
	case <-ctx.Done():
		err = errors.Join(err, fmt.Errorf("managed: supervisor did not release within %s", killBudget))
	}
	return err
}

// Release detaches this process from the SDK lifecycle without sending a signal
// to the child process group. It is used by keep-running dependency stacks: the
// spawned Codefly CLI should survive this SDK handle, but this parent process
// should stop intercepting signals on its behalf.
func (mp *managedProcess) Release() {
	mp.stopSignalTrap()
}

func (mp *managedProcess) stopSignalTrap() {
	defer func() {
		// signal.Stop on an already-stopped channel can panic on repeat
		// close — guard against that.
		recover()
	}()
	if mp.sigCh != nil {
		signal.Stop(mp.sigCh)
		close(mp.sigCh)
	}
}

// readLine is a test-only helper that blocks until one line is available
// on the child's stdout, or the timeout elapses. It returns the line
// WITHOUT the trailing newline.
func (mp *managedProcess) readLine(t interface{ Fatalf(string, ...any) }, timeout time.Duration) string {
	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		line, err := mp.stdoutR.ReadString('\n')
		if err != nil {
			errCh <- err
			return
		}
		lineCh <- line
	}()
	select {
	case line := <-lineCh:
		return line
	case err := <-errCh:
		t.Fatalf("readLine: %v", err)
		return ""
	case <-time.After(timeout):
		t.Fatalf("readLine: timeout after %s", timeout)
		return ""
	}
}

// echoPipe copies r to w line by line until r returns an error. It's the
// per-stream goroutine that decouples the child's stdout/stderr FDs from
// the parent's os.Stdout/Stderr, letting `go test` exit cleanly.
func echoPipe(r *bufio.Reader, w io.Writer) {
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			_, _ = w.Write([]byte(line))
		}
		if err != nil {
			return
		}
	}
}
