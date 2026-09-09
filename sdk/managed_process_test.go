// Tests for the managedProcess lifecycle primitive.
//
// What we want from managedProcess:
//  1. When we start a child, the child AND all of its descendants live
//     in a dedicated process group. Killing that group kills everyone.
//  2. When we call Kill(), the entire group dies within a short bounded
//     time — no orphans, no zombies. The group is the unit of cleanup:
//     a descendant that ignores SIGTERM is escalated to SIGKILL even
//     when its parent honoured SIGTERM and was reaped immediately.
//  3. When the child exits on its own, our internal Wait goroutine reaps
//     it so we don't leak zombies and so inherited file descriptors
//     (os.Stdout/Stderr) get released — otherwise `go test` hangs for
//     WaitDelay waiting for those FDs.
//  4. A signal caught by the parent process triggers Kill(), so
//     pressing Ctrl-C during `go test` doesn't orphan the group.
//
// The tests use tiny `sh -c` scripts as the child so we can verify group
// kill without touching real codefly infrastructure. Nothing here proves
// anything about Docker containers: those are children of the Docker
// daemon, not members of this process group, and no OS signal removes
// them.
//
// Process identity note: these tests, and the cleanup they exercise, rely
// on the Unix guarantee that a pid number stays allocated while it is in
// use as a process-group id. Linux holds a reference to the `struct pid`
// for PIDTYPE_PGID; XNU's fork1 rejects a candidate pid that pgfind still
// resolves. So a pgid whose leader has been reaped cannot be recycled
// while descendants keep the group non-empty — and once the group does
// empty, cleanup signals authenticated member incarnations rather than a
// bare -pgid, so a recycled pgid is never signalled either way.
package sdk

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// killDeadline is the outer bound we hold Kill() to in tests. The
// implementation's own budget is larger; a Kill that needs anywhere near
// this long is a regression.
const killDeadline = 10 * time.Second

// termResistantScript backgrounds a shell that ignores SIGTERM, prints its
// own PID once the trap is installed, and then waits. The launcher execs
// `sleep`, so the group leader dies promptly on SIGTERM while the
// descendant — reparented away — survives it. This is the F06 shape.
const termResistantScript = `
sh -c 'trap "" TERM; echo $$; while :; do sleep 1; done' &
exec sleep 300
`

// pidAlive returns true if the given PID is a live process (not a zombie).
// On Unix, `kill -0 pid` returns success if the process exists and the
// caller has permission to signal it.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// FindProcess on Unix always returns a Process struct without checking.
	// Signal 0 is the probe.
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// waitFor returns true if cond becomes true within d; polls every 50ms.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

// reapTestGroup registers a last-resort SIGKILL of the group the test owns,
// so a failed assertion never leaves a term-resistant descendant behind.
func reapTestGroup(t *testing.T, mp *managedProcess) {
	t.Helper()
	pgid := mp.group.PGID()
	t.Cleanup(func() {
		_ = mp.Kill()
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	})
}

// openDescriptors counts the descriptors this process holds. /dev/fd is the
// per-process descriptor directory on both Linux (a symlink to
// /proc/self/fd) and Darwin.
func openDescriptors(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Skipf("cannot enumerate open descriptors: %v", err)
	}
	return len(entries)
}

func TestManagedProcess_StartAndKillEntireGroup(t *testing.T) {
	// Spawn a parent shell that backgrounds two sleeps and then sleeps
	// itself. The parent's PID is captured via echo, and both child PIDs
	// are captured on stdout. After Kill, all three must be dead.
	//
	// sh -c '
	//   sleep 30 &
	//   echo $!
	//   sleep 30 &
	//   echo $!
	//   sleep 30
	// '
	script := `
sleep 30 &
echo $!
sleep 30 &
echo $!
exec sleep 30
`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mp, err := startManaged(ctx, exec.CommandContext(ctx, "sh", "-c", script))
	if err != nil {
		t.Fatalf("startManaged: %v", err)
	}
	reapTestGroup(t, mp)

	// Read the two child PIDs from stdout. managedProcess captures
	// stdout into a pipe we can read from.
	child1PID, child2PID := readTwoPIDs(t, mp)

	// All three processes should be alive now: the sh parent and its two
	// backgrounded sleeps.
	parentPID := mp.cmd.Process.Pid
	if !pidAlive(parentPID) {
		t.Fatalf("parent PID %d not alive right after start", parentPID)
	}
	if !pidAlive(child1PID) {
		t.Errorf("child1 PID %d not alive", child1PID)
	}
	if !pidAlive(child2PID) {
		t.Errorf("child2 PID %d not alive", child2PID)
	}

	// Process group should equal parent PID because Setpgid=true.
	pgid, err := syscall.Getpgid(parentPID)
	if err != nil {
		t.Fatalf("Getpgid(%d): %v", parentPID, err)
	}
	if pgid != parentPID {
		t.Errorf("process group id = %d, want %d (parent is not group leader)", pgid, parentPID)
	}

	// Every member honours SIGTERM here, so Kill must return with the whole
	// group already gone — no polling grace afterwards.
	if err := mp.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if pidAlive(parentPID) {
		t.Errorf("parent %d still alive when Kill returned", parentPID)
	}
	if pidAlive(child1PID) {
		t.Errorf("child1 %d still alive when Kill returned", child1PID)
	}
	if pidAlive(child2PID) {
		t.Errorf("child2 %d still alive when Kill returned", child2PID)
	}
}

// TestManagedProcess_KillReapsTermResistantDescendant is the F06 regression:
// the group leader exits promptly on SIGTERM while a descendant ignores it.
// Escalation must be driven by group liveness, not by the leader having been
// reaped, or the descendant survives cleanup.
func TestManagedProcess_KillReapsTermResistantDescendant(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mp, err := startManaged(ctx, exec.CommandContext(ctx, "sh", "-c", termResistantScript))
	if err != nil {
		t.Fatalf("startManaged: %v", err)
	}
	reapTestGroup(t, mp)

	// The descendant announces itself only after installing the trap, so
	// reading this line is the readiness proof — no sleeps.
	descendantPID := readPID(t, mp)
	leaderPID := mp.cmd.Process.Pid

	if !pidAlive(descendantPID) {
		t.Fatalf("descendant %d not alive after announcing itself", descendantPID)
	}
	if group, err := syscall.Getpgid(descendantPID); err != nil || group != leaderPID {
		t.Fatalf("descendant %d process group = %d (err %v), want %d", descendantPID, group, err, leaderPID)
	}

	started := time.Now()
	if err := mp.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if elapsed := time.Since(started); elapsed > killDeadline {
		t.Errorf("Kill took %s, want under %s", elapsed, killDeadline)
	}

	if pidAlive(leaderPID) {
		t.Errorf("leader %d still alive when Kill returned", leaderPID)
	}
	if pidAlive(descendantPID) {
		t.Errorf("term-resistant descendant %d survived Kill", descendantPID)
	}
	select {
	case <-mp.Done():
	default:
		t.Error("supervisor had not completed when Kill returned")
	}
}

// TestManagedProcess_KillAfterStartupCancellation covers cancellation during
// startup: exec.CommandContext kills only the leader, so cleanup still has to
// find and terminate the surviving group on its own.
func TestManagedProcess_KillAfterStartupCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mp, err := startManaged(ctx, exec.CommandContext(ctx, "sh", "-c", termResistantScript))
	if err != nil {
		t.Fatalf("startManaged: %v", err)
	}
	reapTestGroup(t, mp)

	descendantPID := readPID(t, mp)
	leaderPID := mp.cmd.Process.Pid

	cancel()
	if !waitFor(killDeadline, func() bool { return !pidAlive(leaderPID) }) {
		t.Fatalf("leader %d survived context cancellation", leaderPID)
	}

	started := time.Now()
	if err := mp.Kill(); err != nil {
		t.Fatalf("Kill after cancellation: %v", err)
	}
	if elapsed := time.Since(started); elapsed > killDeadline {
		t.Errorf("Kill took %s, want under %s", elapsed, killDeadline)
	}
	if pidAlive(descendantPID) {
		t.Errorf("descendant %d survived cleanup after cancellation", descendantPID)
	}
}

func TestManagedProcess_ChildExitsCleanly(t *testing.T) {
	// A child that exits immediately. The Wait goroutine should reap it
	// and release its file descriptors without the test hanging.
	ctx := context.Background()
	mp, err := startManaged(ctx, exec.CommandContext(ctx, "sh", "-c", "exit 0"))
	if err != nil {
		t.Fatalf("startManaged: %v", err)
	}

	// The process should be reaped by the internal Wait goroutine within
	// a bounded time.
	if !waitFor(2*time.Second, func() bool {
		mp.mu.Lock()
		defer mp.mu.Unlock()
		return mp.exited
	}) {
		t.Errorf("child did not get reaped within 2s")
	}

	// Kill after it already exited must be a harmless no-op.
	started := time.Now()
	if err := mp.Kill(); err != nil {
		t.Errorf("Kill on already-exited process should be nil, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > killDeadline {
		t.Errorf("Kill on an exited process took %s, want under %s", elapsed, killDeadline)
	}
}

func TestManagedProcess_KillIdempotent(t *testing.T) {
	ctx := context.Background()
	mp, err := startManaged(ctx, exec.CommandContext(ctx, "sh", "-c", "exec sleep 30"))
	if err != nil {
		t.Fatalf("startManaged: %v", err)
	}
	if err := mp.Kill(); err != nil {
		t.Errorf("first Kill: %v", err)
	}
	if err := mp.Kill(); err != nil {
		t.Errorf("second Kill should be idempotent, got %v", err)
	}
	if err := mp.Kill(); err != nil {
		t.Errorf("third Kill should be idempotent, got %v", err)
	}
}

// TestManagedProcess_ConcurrentKill asserts that every concurrent caller
// blocks until the single teardown finishes and observes its result.
func TestManagedProcess_ConcurrentKill(t *testing.T) {
	ctx := context.Background()
	mp, err := startManaged(ctx, exec.CommandContext(ctx, "sh", "-c", termResistantScript))
	if err != nil {
		t.Fatalf("startManaged: %v", err)
	}
	reapTestGroup(t, mp)

	descendantPID := readPID(t, mp)

	const callers = 8
	errs := make([]error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := range errs {
		go func() {
			defer wg.Done()
			errs[i] = mp.Kill()
		}()
	}
	returned := make(chan struct{})
	go func() {
		wg.Wait()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(killDeadline):
		t.Fatalf("concurrent Kill did not return within %s", killDeadline)
	}

	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent Kill %d: %v", i, err)
		}
	}
	if pidAlive(descendantPID) {
		t.Errorf("descendant %d survived concurrent Kill", descendantPID)
	}
}

// TestManagedProcess_RepeatedRunsDoNotLeak asserts the supervisor, signal and
// stream goroutines plus their pipes are all released by Kill, over enough
// iterations that a per-run leak would show.
func TestManagedProcess_RepeatedRunsDoNotLeak(t *testing.T) {
	ctx := context.Background()
	run := func() {
		mp, err := startManaged(ctx, exec.CommandContext(ctx, "sh", "-c", termResistantScript))
		if err != nil {
			t.Fatalf("startManaged: %v", err)
		}
		reapTestGroup(t, mp)
		_ = readPID(t, mp)
		if err := mp.Kill(); err != nil {
			t.Fatalf("Kill: %v", err)
		}
	}

	// One warm-up run so lazily-initialised runtime state isn't counted.
	run()
	goroutinesBefore := settledGoroutines(t)
	descriptorsBefore := openDescriptors(t)

	for range 4 {
		run()
	}

	// Teardown goroutines are scheduled out asynchronously, so a snapshot
	// taken the instant the last Kill returns can still see stragglers.
	// Growth that never drains is the leak; a transient is not.
	if !waitFor(10*time.Second, func() bool { return runtime.NumGoroutine() <= goroutinesBefore }) {
		t.Errorf("goroutines grew over repeated runs: before=%d after=%d", goroutinesBefore, runtime.NumGoroutine())
	}
	if !waitFor(10*time.Second, func() bool { return openDescriptors(t) <= descriptorsBefore }) {
		t.Errorf("open descriptors grew over repeated runs: before=%d after=%d", descriptorsBefore, openDescriptors(t))
	}
}

// settledGoroutines returns the goroutine count once it has held steady, so a
// baseline is never taken mid-teardown of an earlier run.
func settledGoroutines(t *testing.T) int {
	t.Helper()
	const stableSamples = 5
	stable, last := 0, -1
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		current := runtime.NumGoroutine()
		if current == last {
			if stable++; stable == stableSamples {
				return current
			}
		} else {
			stable, last = 0, current
		}
		time.Sleep(100 * time.Millisecond)
	}
	return runtime.NumGoroutine()
}

// readPID reads one integer from mp's captured stdout.
func readPID(t *testing.T, mp *managedProcess) int {
	t.Helper()
	line := mp.readLine(t, 5*time.Second)
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("parse pid %q: %v", line, err)
	}
	return pid
}

// readTwoPIDs reads two integers from mp's captured stdout, one per line,
// and returns them. Fatal's on malformed output.
func readTwoPIDs(t *testing.T, mp *managedProcess) (int, int) {
	t.Helper()
	// The child writes two PIDs then blocks in sleep. We read line-by-line
	// via the captured stdout reader.
	return readPID(t, mp), readPID(t, mp)
}
