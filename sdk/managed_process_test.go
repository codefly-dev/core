// Tests for the managedProcess lifecycle primitive.
//
// What we want from managedProcess:
//   1. When we start a child, the child AND all of its descendants live
//      in a dedicated process group. Killing that group kills everyone.
//   2. When we call Kill(), the entire group dies within a short bounded
//      time — no orphans, no zombies.
//   3. When the child exits on its own, our internal Wait goroutine reaps
//      it so we don't leak zombies and so inherited file descriptors
//      (os.Stdout/Stderr) get released — otherwise `go test` hangs for
//      WaitDelay waiting for those FDs.
//   4. A signal caught by the parent process triggers Kill(), so
//      pressing Ctrl-C during `go test` doesn't orphan containers.
//
// The tests use a tiny `sh -c` script as the child (nested sleeps) so
// we can verify group kill without touching real codefly infrastructure.
package sdk

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

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

	// Kill the group. Everything should be dead within 2 seconds.
	if err := mp.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	if !waitFor(2*time.Second, func() bool { return !pidAlive(parentPID) }) {
		t.Errorf("parent %d still alive 2s after Kill", parentPID)
	}
	if !waitFor(2*time.Second, func() bool { return !pidAlive(child1PID) }) {
		t.Errorf("child1 %d still alive 2s after Kill", child1PID)
	}
	if !waitFor(2*time.Second, func() bool { return !pidAlive(child2PID) }) {
		t.Errorf("child2 %d still alive 2s after Kill", child2PID)
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
	if err := mp.Kill(); err != nil {
		t.Errorf("Kill on already-exited process should be nil, got %v", err)
	}
}

func TestManagedProcess_KillIdempotent(t *testing.T) {
	ctx := context.Background()
	mp, err := startManaged(ctx, exec.CommandContext(ctx, "sh", "-c", "sleep 30"))
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

// readTwoPIDs reads two integers from mp's captured stdout, one per line,
// and returns them. Fatal's on malformed output.
func readTwoPIDs(t *testing.T, mp *managedProcess) (int, int) {
	t.Helper()
	// The child writes two PIDs then blocks in sleep. We read line-by-line
	// via the captured stdout reader.
	line1 := mp.readLine(t, 2*time.Second)
	line2 := mp.readLine(t, 2*time.Second)
	p1, err := strconv.Atoi(strings.TrimSpace(line1))
	if err != nil {
		t.Fatalf("parse pid1 %q: %v", line1, err)
	}
	p2, err := strconv.Atoi(strings.TrimSpace(line2))
	if err != nil {
		t.Fatalf("parse pid2 %q: %v", line2, err)
	}
	return p1, p2
}
