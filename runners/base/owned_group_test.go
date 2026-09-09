package base

import (
	"bufio"
	"context"
	"errors"
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

func startTermResistantGroup(t *testing.T) (*OwnedProcessGroup, int) {
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

	// The leader has to be reaped by somebody, exactly as the SDK's
	// supervisor goroutine does, or its zombie keeps the group alive.
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	// The last-resort reap runs even when an assertion fails, so a broken
	// implementation fails the test instead of stranding a resistant
	// descendant or blocking the test on cmd.Wait. Signalling the bare pgid
	// is safe here in a way it is not in production: the group was started
	// moments ago in this same test, and recycling the pgid would take a full
	// pid-space wrap (~100k forks on Darwin, ~4M on Linux) in that window.
	t.Cleanup(func() {
		_ = syscall.Kill(-group.PGID(), syscall.SIGKILL)
		select {
		case <-waited:
		case <-time.After(10 * time.Second):
			t.Error("leader was never reaped")
		}
	})

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read descendant announcement: %v", err)
	}
	descendant, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("parse descendant pid %q: %v", line, err)
	}
	return group, descendant
}

func TestOwnedProcessGroupTerminatesTermResistantDescendant(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	group, descendant := startTermResistantGroup(t)

	if !IsProcessAlive(descendant) {
		t.Fatalf("descendant %d not alive after announcing itself", descendant)
	}

	if err := group.Terminate(context.Background(), sigtermGrace); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if IsProcessAlive(descendant) {
		t.Errorf("term-resistant descendant %d survived Terminate", descendant)
	}
	if isProcessGroupAlive(group.PGID()) {
		t.Errorf("process group %d still alive after Terminate", group.PGID())
	}
}

// TestOwnedProcessGroupLeavesNoRegistryRecord pins the difference from
// StartTrackedProcessGroup: an owned group is reapable by its owner only, so
// releasing it must not leave a record a later reaper would act on.
func TestOwnedProcessGroupLeavesNoRegistryRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	group, _ := startTermResistantGroup(t)

	dir, err := pgidStateDir()
	if err != nil {
		t.Fatalf("pgidStateDir: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, strconv.Itoa(group.PGID())+".pgid")); !os.IsNotExist(err) {
		t.Errorf("owned process group published a registry record: %v", err)
	}

	if err := group.Terminate(context.Background(), sigtermGrace); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
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
	if err := group.Terminate(context.Background(), sigtermGrace); err != nil {
		t.Errorf("Terminate on an exited group: %v", err)
	}
}

// TestAuthenticateOwnedProcessGroupRejectsRecycledLeader covers process
// identity reuse: a live process sitting on our pgid that is not the leader we
// recorded means the pid was recycled, and the group must not be signalled.
func TestAuthenticateOwnedProcessGroupRejectsRecycledLeader(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	group, descendant := startTermResistantGroup(t)
	defer func() {
		if err := group.Terminate(context.Background(), sigtermGrace); err != nil {
			t.Errorf("Terminate: %v", err)
		}
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
	// A pgid that provably names a different group means ours emptied: that is
	// a completed teardown, not a failure to report.
	recycledGroup := &OwnedProcessGroup{record: recycled}
	if err := recycledGroup.Terminate(context.Background(), sigtermGrace); err != nil {
		t.Errorf("Terminate on a recycled pgid = %v, want nil", err)
	}
	if !IsProcessAlive(descendant) {
		t.Error("Terminate on a recycled pgid signalled the group it refused to authenticate")
	}
}

// TestOwnedProcessGroupTerminateIsBounded asserts the escalation returns
// within its own budget rather than blocking on a resistant descendant.
func TestOwnedProcessGroupTerminateIsBounded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	group, _ := startTermResistantGroup(t)

	started := time.Now()
	if err := group.Terminate(context.Background(), sigtermGrace); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	budget := sigtermGrace + killSweepInterval + sigkillGrace + time.Second
	if elapsed := time.Since(started); elapsed > budget {
		t.Errorf("Terminate took %s, want under %s", elapsed, budget)
	}
}

// unreachableIdentity names a process that openProcessSignalHandle cannot open
// and cannot classify as gone: pid 0 fails the identity read on Darwin and
// pidfd_open with EINVAL on Linux, so it produces the hard error that used to
// abort a whole signal pass.
func unreachableIdentity() processIdentity {
	return processIdentity{pid: 0}
}

// TestSignalProcessIdentitiesDeliversDespiteUnreachableMember is the
// regression for the pass that gave up: one member we cannot open must not
// spare the rest of the group. The old code returned before delivering a
// single signal, so a group containing one unreachable process survived
// cleanup entirely.
func TestSignalProcessIdentitiesDeliversDespiteUnreachableMember(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := exec.Command("sh", "-c", "exec sleep 300")
	group, err := StartOwnedProcessGroup(cmd)
	if err != nil {
		t.Fatalf("StartOwnedProcessGroup: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = syscall.Kill(-group.PGID(), syscall.SIGKILL)
		<-waited
	})

	members, err := inspectProcessGroup(context.Background(), group.PGID())
	if err != nil || len(members) != 1 {
		t.Fatalf("inspectProcessGroup: members=%d err=%v", len(members), err)
	}
	live := members[0]

	signalErr := signalProcessIdentities(context.Background(),
		[]processIdentity{unreachableIdentity(), live}, syscall.SIGKILL)
	if signalErr == nil {
		t.Error("an unreachable member should be reported, got nil")
	}
	if errors.Is(signalErr, errProcessGroupNotSignalable) {
		t.Errorf("a group with one reachable member is signalable, got %v", signalErr)
	}
	if !waitForGroupDeath(context.Background(), group.PGID(), sigkillGrace) {
		t.Errorf("live member %d survived a pass that also held an unreachable member", live.pid)
	}
}

// TestTerminateEscalatesDespiteUnreachableMember is the same regression one
// level up: a partial failure on the SIGTERM pass must not cancel the SIGKILL
// escalation that the resistant descendant depends on.
func TestTerminateEscalatesDespiteUnreachableMember(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	group, descendant := startTermResistantGroup(t)

	unreachable := func(ctx context.Context, rec pgidRecord) ([]processIdentity, bool, error) {
		members, authenticated, err := authenticateOwnedProcessGroup(ctx, rec)
		if !authenticated {
			return members, authenticated, err
		}
		return append([]processIdentity{unreachableIdentity()}, members...), true, err
	}

	if err := terminateGroup(context.Background(), group.record, unreachable, sigtermGrace); err != nil {
		t.Errorf("terminateGroup with an unreachable member: %v", err)
	}
	if IsProcessAlive(descendant) {
		t.Errorf("descendant %d survived because one member could not be opened", descendant)
	}
}

// TestTerminateGroupOfUnreapedZombiesSucceeds covers the leader that exits
// before its supervisor reaps it: the zombie holds the pgid, so the group
// reads as alive while nothing in it can be inspected. That is "nothing to
// signal", not "not our group" — treating it as an identity failure made
// Terminate return an error instantly without escalating.
func TestTerminateGroupOfUnreapedZombiesSucceeds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := exec.Command("sh", "-c", "exit 0")
	group, err := StartOwnedProcessGroup(cmd)
	if err != nil {
		t.Fatalf("StartOwnedProcessGroup: %v", err)
	}
	// Deliberately unreaped: the leader is a zombie holding the pgid.
	if !waitFor(5*time.Second, func() bool {
		members, _ := inspectProcessGroup(context.Background(), group.PGID())
		return isProcessGroupAlive(group.PGID()) && len(members) == 0
	}) {
		t.Skip("could not observe an unreaped zombie holding the process group")
	}

	terminated := make(chan error, 1)
	go func() { terminated <- group.Terminate(context.Background(), 2*time.Second) }()
	// Reap it the way the SDK's supervisor does, while Terminate is waiting.
	_ = cmd.Wait()
	select {
	case err := <-terminated:
		if err != nil {
			t.Errorf("Terminate over a zombie-only group: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Terminate did not return")
	}
}

// TestProcessEnvironmentReadableMatchesReality pins the platform capability
// that decides how a leaderless group is authenticated. If this constant is
// wrong, Linux silently loses the credential check and Darwin silently refuses
// to terminate groups it owns.
func TestProcessEnvironmentReadableMatchesReality(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := exec.Command("sh", "-c", "exec sleep 300")
	group, err := StartOwnedProcessGroup(cmd)
	if err != nil {
		t.Fatalf("StartOwnedProcessGroup: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = syscall.Kill(-group.PGID(), syscall.SIGKILL)
		<-waited
	})

	// The credential read races the child's exec: Darwin can fail the
	// procargs read outright for a process that is mid-exec. The capability
	// under test is stable, the read is not, so poll for a clean one.
	var value string
	var readErr error
	if !waitFor(5*time.Second, func() bool {
		value, readErr = readProcessGroupAuthentication(group.PGID())
		return readErr == nil
	}) {
		t.Fatalf("readProcessGroupAuthentication: %v", readErr)
	}
	readable := value == group.record.Authentication
	if readable != processEnvironmentReadable() {
		t.Errorf("processEnvironmentReadable() = %t but a credentialed child's environment %s",
			processEnvironmentReadable(),
			map[bool]string{true: "was readable", false: "was not readable"}[readable])
	}
}

// waitFor polls cond until it holds or d elapses.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}
