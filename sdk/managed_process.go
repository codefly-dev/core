package sdk

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// managedProcess wraps an exec.Cmd with a lifecycle that survives the
// rough edges of running long-lived subprocesses from a Go test binary:
//
//  1. The child runs in its own process group (Setpgid=true) so all of
//     its descendants can be killed with a single group signal. Without
//     this, killing the shell/CLI leaves its containers and grandchildren
//     behind as orphans.
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
//     (e.g., tests) can lock mu.
//
//  4. An OS signal trap converts SIGINT / SIGTERM / SIGHUP into a call
//     to Kill() so Ctrl-C during `go test` doesn't orphan the group.
//     The trap is installed once per managedProcess and removed on Kill.
//
// The struct is intentionally independent of the codefly CLI so it can
// be unit-tested with any command (see managed_process_test.go).
type managedProcess struct {
	cmd *exec.Cmd

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
	killed     bool
	sigCh      chan os.Signal
	waitedOnce sync.Once
	waitErr    error
}

// startManaged starts cmd in its own process group with the lifecycle
// guarantees described on managedProcess. The returned process is already
// running — the internal supervisor and signal-trap goroutines are live.
//
// Callers are expected to call Kill() (or let the signal trap do it)
// during teardown. It is safe to call Kill multiple times.
func startManaged(_ any, cmd *exec.Cmd) (*managedProcess, error) {
	// Put the child in its own process group so we can kill the whole
	// tree with one signal. SysProcAttr may already be set by the caller
	// (e.g., to pass environment tweaks) — we only touch Setpgid.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

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

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("managed: Start: %w", err)
	}

	mp := &managedProcess{
		cmd:       cmd,
		stdoutR:   bufio.NewReader(stdoutPipe),
		stderrR:   bufio.NewReader(stderrPipe),
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
	})
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

// Kill terminates the entire process group of the managed child. It is
// idempotent — subsequent calls are no-ops.
//
// Sequence:
//  1. Send SIGTERM to the whole group (pgid = child's PID).
//  2. Wait up to 2s for the child to exit cleanly.
//  3. If still alive, send SIGKILL to the group.
//  4. Ensure the supervise goroutine has completed.
//  5. Stop the signal trap.
func (mp *managedProcess) Kill() error {
	mp.mu.Lock()
	if mp.killed {
		mp.mu.Unlock()
		return nil
	}
	mp.killed = true
	mp.mu.Unlock()

	if mp.cmd.Process == nil {
		return nil
	}
	pgid := mp.cmd.Process.Pid

	// Graceful first: SIGTERM to the group.
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	// Poll for exit with a 2s budget.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mp.mu.Lock()
		exited := mp.exited
		mp.mu.Unlock()
		if exited {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Hard kill if graceful didn't work.
	mp.mu.Lock()
	exited := mp.exited
	mp.mu.Unlock()
	if !exited {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}

	// Stop the signal trap so the signal goroutine releases.
	mp.stopSignalTrap()

	return nil
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
