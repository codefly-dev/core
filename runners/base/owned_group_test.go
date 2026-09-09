package base

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// termResistantGroupScript backgrounds a shell that ignores SIGTERM and
// announces its PID once the trap is installed, then execs `sleep` so the
// group leader itself dies promptly on SIGTERM. Terminating this group is
// only possible by escalating on group liveness rather than on the leader.
const termResistantGroupScript = `
sh -c 'trap "" TERM; echo $$; while :; do sleep 1; done' &
exec sleep 300
`

func startTermResistantGroup(t *testing.T) (*TrackedProcessGroup, int, func() error) {
	t.Helper()
	cmd := exec.Command("sh", "-c", termResistantGroupScript)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	group, err := StartOwnedProcessGroup(cmd)
	if err != nil {
		t.Fatalf("StartOwnedProcessGroup: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-group.PGID(), syscall.SIGKILL)
	})

	// The leader has to be reaped by somebody, exactly as the SDK's
	// supervisor goroutine does, or its zombie keeps the group alive.
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read descendant announcement: %v", err)
	}
	descendant, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("parse descendant pid %q: %v", line, err)
	}
	return group, descendant, func() error { return <-waited }
}

func TestOwnedProcessGroupTerminatesTermResistantDescendant(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	group, descendant, wait := startTermResistantGroup(t)

	if !IsProcessAlive(descendant) {
		t.Fatalf("descendant %d not alive after announcing itself", descendant)
	}

	if err := group.Terminate(context.Background()); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if IsProcessAlive(descendant) {
		t.Errorf("term-resistant descendant %d survived Terminate", descendant)
	}
	if isProcessGroupAlive(group.PGID()) {
		t.Errorf("process group %d still alive after Terminate", group.PGID())
	}
	_ = wait()
}

// TestOwnedProcessGroupLeavesNoRegistryRecord pins the difference from
// StartTrackedProcessGroup: an owned group is reapable by its owner only, so
// releasing it must not leave a record a later reaper would act on.
func TestOwnedProcessGroupLeavesNoRegistryRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	group, _, wait := startTermResistantGroup(t)

	dir, err := pgidStateDir()
	if err != nil {
		t.Fatalf("pgidStateDir: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, strconv.Itoa(group.PGID())+".pgid")); !os.IsNotExist(err) {
		t.Errorf("owned process group published a registry record: %v", err)
	}

	if err := group.Terminate(context.Background()); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	_ = wait()
}

func TestTerminateOwnedProcessGroupAfterNaturalExit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := exec.Command("sh", "-c", "exit 0")
	group, err := StartOwnedProcessGroup(cmd)
	if err != nil {
		t.Fatalf("StartOwnedProcessGroup: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if err := group.Terminate(context.Background()); err != nil {
		t.Errorf("Terminate on an exited group: %v", err)
	}
}

// TestAuthenticateOwnedProcessGroupRejectsRecycledLeader covers process
// identity reuse: a live process sitting on our pgid that is not the leader we
// recorded means the pid was recycled, and the group must not be signalled.
func TestAuthenticateOwnedProcessGroupRejectsRecycledLeader(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	group, _, wait := startTermResistantGroup(t)
	defer func() {
		if err := group.Terminate(context.Background()); err != nil {
			t.Errorf("Terminate: %v", err)
		}
		_ = wait()
	}()

	if _, authenticated, err := authenticateOwnedProcessGroup(context.Background(), group.record); err != nil || !authenticated {
		t.Fatalf("live owned group did not authenticate: authenticated=%v err=%v", authenticated, err)
	}

	recycled := group.record
	recycled.Leader.StartID++
	_, authenticated, err := authenticateOwnedProcessGroup(context.Background(), recycled)
	if err != nil {
		t.Fatalf("authenticate recycled leader: %v", err)
	}
	if authenticated {
		t.Error("a leader whose start identity changed was accepted as ours")
	}
	if err := signalGroup(context.Background(), recycled, authenticateOwnedProcessGroup, syscall.SIGKILL); err != errProcessGroupIdentityChanged {
		t.Errorf("signalGroup on a recycled pgid = %v, want %v", err, errProcessGroupIdentityChanged)
	}
}

// TestOwnedProcessGroupTerminateIsBounded asserts the escalation returns
// within its own budget rather than blocking on a resistant descendant.
func TestOwnedProcessGroupTerminateIsBounded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	group, _, wait := startTermResistantGroup(t)

	started := time.Now()
	if err := group.Terminate(context.Background()); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	budget := sigtermGrace + killSweeps*sigkillGrace + time.Second
	if elapsed := time.Since(started); elapsed > budget {
		t.Errorf("Terminate took %s, want under %s", elapsed, budget)
	}
	_ = wait()
}
