package base

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

const (
	processGroupRoleEnv        = "CODEFLY_PROCESS_GROUP_TEST_ROLE"
	processGroupPIDFileEnv     = "CODEFLY_PROCESS_GROUP_TEST_PID_FILE"
	processGroupReadyFileEnv   = "CODEFLY_PROCESS_GROUP_TEST_READY_FILE"
	processGroupReleaseFileEnv = "CODEFLY_PROCESS_GROUP_TEST_RELEASE_FILE"
	processGroupLeaderReadyEnv = "CODEFLY_PROCESS_GROUP_TEST_LEADER_READY_FILE"
)

func TestProcessGroupRegistryHelper(t *testing.T) {
	switch os.Getenv(processGroupRoleEnv) {
	case "member":
		writeTestFile(t, os.Getenv(processGroupReadyFileEnv), "ready")
		waitForTerminationSignal()
	case "ignores-term":
		signal.Ignore(syscall.SIGTERM)
		writeTestFile(t, os.Getenv(processGroupReadyFileEnv), "ready")
		select {}
	case "owner":
		leader := registryHelperCommand("member")
		leader.Env = append(leader.Env, processGroupReadyFileEnv+"="+os.Getenv(processGroupReadyFileEnv))
		if _, err := StartTrackedProcessGroup(leader); err != nil {
			t.Fatal(err)
		}
		pid := leader.Process.Pid
		writeTestFile(t, os.Getenv(processGroupPIDFileEnv), strconv.Itoa(pid))
		waitForTestFile(t, os.Getenv(processGroupReadyFileEnv))
	case "leaderless-owner":
		leader := registryHelperCommand("leader-with-member")
		leader.Env = append(leader.Env,
			processGroupReadyFileEnv+"="+os.Getenv(processGroupReadyFileEnv),
			processGroupLeaderReadyEnv+"="+os.Getenv(processGroupLeaderReadyEnv),
			processGroupReleaseFileEnv+"="+os.Getenv(processGroupReleaseFileEnv))
		group, err := StartTrackedProcessGroup(leader)
		if err != nil {
			t.Fatal(err)
		}
		pid := leader.Process.Pid
		waitForTestFile(t, os.Getenv(processGroupLeaderReadyEnv))
		writeTestFile(t, os.Getenv(processGroupPIDFileEnv), strconv.Itoa(pid))
		writeTestFile(t, os.Getenv(processGroupReleaseFileEnv), "release")
		if err := leader.Wait(); err != nil {
			t.Fatal(err)
		}
		if err := group.RemoveIfDead(); err != nil {
			t.Fatal(err)
		}
	case "leader-with-member":
		writeTestFile(t, os.Getenv(processGroupLeaderReadyEnv), "ready")
		waitForTestFile(t, os.Getenv(processGroupReleaseFileEnv))
		member := registryHelperCommand("member")
		member.Env = append(member.Env, processGroupReadyFileEnv+"="+os.Getenv(processGroupReadyFileEnv))
		if err := member.Start(); err != nil {
			t.Fatal(err)
		}
		waitForTestFile(t, os.Getenv(processGroupReadyFileEnv))
	}
}

func TestInspectProcessGroupMemberRejectsNonPositiveEnumeratorEntries(t *testing.T) {
	// Explicitly inject the transient invalid observations seen from the real
	// Darwin process enumerator. Calling Getpgid(0) would inspect this test's
	// process group, so both values must be rejected before any syscall.
	for _, rawPID := range []int32{0, -1} {
		identity, matched, err := inspectProcessGroupMember(rawPID, syscall.Getpgrp())
		if err != nil {
			t.Fatalf("PID %d returned error: %v", rawPID, err)
		}
		if matched || identity != (processIdentity{}) {
			t.Fatalf("PID %d matched as %+v", rawPID, identity)
		}
	}
}

func TestReaperPreservesGroupOwnedByLiveProcess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, leader)
	pid := leader.Process.Pid

	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGroupAlive(t, pid)
	if _, err := os.Stat(recordPath(t, pid)); err != nil {
		t.Fatalf("live owner's record was removed: %v", err)
	}
}

func TestReaperReapsAuthenticatedOrphan(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid, path := spawnOrphanedGroup(t, false)
	defer cleanupOrphanedGroup(pid, path)

	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGroupDead(t, pid)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reaped record still exists: %v", err)
	}
}

func TestReaperReapsAuthenticatedLeaderlessDescendant(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid, path := spawnOrphanedGroup(t, true)
	defer cleanupOrphanedGroup(pid, path)
	if _, err := inspectProcess(pid); !errors.Is(err, errProcessNotFound) {
		t.Fatalf("process-group leader %d still exists: %v", pid, err)
	}
	assertGroupAlive(t, pid)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("leader exit cleanup removed a live descendant's record: %v", err)
	}

	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGroupDead(t, pid)
}

func TestReaperRejectsRecordForReusedGroupWithoutSignaling(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, leader)
	pid := leader.Process.Pid
	path := recordPath(t, pid)
	rewriteRecord(t, path, func(rec *pgidRecord) {
		rec.Leader.StartID--
	})

	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGroupAlive(t, pid)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected record still exists: %v", err)
	}
}

func TestReaperRejectsReusedLeaderlessGroupWithoutSignaling(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid, path := spawnOrphanedGroup(t, true)
	defer cleanupOrphanedGroup(pid, path)
	rewriteRecord(t, path, func(rec *pgidRecord) {
		rec.Authentication = strings.Repeat("0", groupAuthBytes*2)
	})

	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGroupAlive(t, pid)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected leaderless record still exists: %v", err)
	}
}

func TestReaperAuthenticatesOwnerBirth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "member")
	pid := leader.Process.Pid
	path := recordPath(t, pid)
	defer func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = leader.Wait()
		_ = os.Remove(path)
	}()
	rewriteRecord(t, path, func(rec *pgidRecord) {
		rec.Owner.StartID--
	})
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- leader.Wait()
	}()

	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-waitResult; err != nil {
		t.Fatal(err)
	}
	assertGroupDead(t, pid)
}

func TestReaperQuarantinesPartialRecordWithoutSignaling(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, leader)
	pid := leader.Process.Pid
	path := recordPath(t, pid)
	partial := []byte(`{"pgid":`)
	if err := os.WriteFile(path, partial, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGroupAlive(t, pid)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial record remained active: %v", err)
	}
	quarantinePath := path + ".invalid"
	data, err := os.ReadFile(quarantinePath)
	if err != nil {
		t.Fatalf("read quarantined record: %v", err)
	}
	if string(data) != string(partial) {
		t.Fatalf("quarantined record = %q, want %q", data, partial)
	}
	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatalf("quarantined record poisoned the next sweep: %v", err)
	}
}

func TestChangedRecordIsRetained(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, leader)
	pid := leader.Process.Pid
	path := recordPath(t, pid)
	_, snapshot, err := readPgidRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(filepath.Dir(path), "replacement")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}

	if err := removeRecord(path, snapshot); err == nil {
		t.Fatal("changed record was removed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("changed record was not retained: %v", err)
	}
}

func TestReaperQuarantinesInvalidRecordAndContinues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	failedPID, failedPath := spawnOrphanedGroup(t, false)
	defer cleanupOrphanedGroup(failedPID, failedPath)
	reapedPID, reapedPath := spawnOrphanedGroup(t, false)
	defer cleanupOrphanedGroup(reapedPID, reapedPath)
	if err := os.WriteFile(failedPath, []byte(`{"pgid":`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGroupAlive(t, failedPID)
	assertGroupDead(t, reapedPID)
	if _, err := os.Stat(failedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid record remained active: %v", err)
	}
	if _, err := os.Stat(failedPath + ".invalid"); err != nil {
		t.Fatalf("invalid record was not retained for forensics: %v", err)
	}
}

func TestReaperCancellationDoesNotEscalateToSIGKILL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "ignores-term")
	pid := leader.Process.Pid
	defer func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = leader.Wait()
	}()
	rec, _, err := readPgidRecord(recordPath(t, pid))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- terminateAuthenticatedGroup(ctx, rec)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("terminateAuthenticatedGroup returned %v", err)
	}
	assertGroupAlive(t, pid)
}

func TestReaperWaitsForCrossProcessRegistryLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := pgidStateDir()
	if err != nil {
		t.Fatal(err)
	}
	lock := flock.New(filepath.Join(dir, pgidLockName))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := ReapStaleProcessGroups(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReapStaleProcessGroups returned %v", err)
	}
}

func TestStartTrackedProcessGroupCapturesIdentityBeforeRegistryLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := pgidStateDir()
	if err != nil {
		t.Fatal(err)
	}
	lock := flock.New(filepath.Join(dir, pgidLockName))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}

	readyPath := filepath.Join(t.TempDir(), "ready")
	command := registryHelperCommand("member")
	command.Env = append(command.Env, processGroupReadyFileEnv+"="+readyPath)
	type startResult struct {
		group *TrackedProcessGroup
		err   error
	}
	result := make(chan startResult, 1)
	go func() {
		group, startErr := StartTrackedProcessGroup(command)
		result <- startResult{group: group, err: startErr}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(registryProcessLock) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(registryProcessLock) == 0 {
		t.Fatal("registration never reached the registry lock")
	}
	if _, err := os.Stat(readyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("process ran before its identity was registered: %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	started := <-result
	if started.err != nil {
		t.Fatalf("registration lost the process birth identity while waiting for the lock: %v", started.err)
	}
	waitForTestFile(t, readyPath)
	path := recordPath(t, command.Process.Pid)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("captured process-group record was not published: %v", err)
	}
	if err := started.group.Signal(context.Background(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := started.group.RemoveIfDead(); err != nil {
		t.Fatal(err)
	}
}

func TestDelayedCleanupDoesNotRemoveReplacementRecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "member")
	path := recordPath(t, leader.Process.Pid)
	replacementAuthentication := strings.Repeat("0", groupAuthBytes*2)
	rewriteRecord(t, path, func(rec *pgidRecord) {
		rec.Authentication = replacementAuthentication
	})
	if err := leader.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := leader.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := leader.group.RemoveIfDead(); err != nil {
		t.Fatal(err)
	}
	rec, _, err := readPgidRecord(path)
	if err != nil {
		t.Fatalf("replacement record was removed: %v", err)
	}
	if rec.Authentication != replacementAuthentication {
		t.Fatalf("authentication = %q, want replacement", rec.Authentication)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestStartTrackedProcessGroupFailsClosedWhenRegistryIsUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".codefly"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := registryHelperCommand("ignores-term")
	command.Env = append(command.Env, processGroupReadyFileEnv+"="+filepath.Join(t.TempDir(), "ready"))
	group, err := StartTrackedProcessGroup(command)
	if err == nil || group != nil {
		t.Fatalf("StartTrackedProcessGroup = (%v, %v), want registration error", group, err)
	}
	if command.ProcessState == nil {
		t.Fatal("unregistered process-group leader was not reaped")
	}
	if signalErr := syscall.Kill(command.Process.Pid, 0); !errors.Is(signalErr, syscall.ESRCH) {
		t.Fatalf("unregistered process is still signalable: %v", signalErr)
	}
}

func TestExpiredProcessSignalHandleDoesNotRetarget(t *testing.T) {
	readyPath := filepath.Join(t.TempDir(), "ready")
	target := registryHelperCommand("member")
	target.Env = append(target.Env, processGroupReadyFileEnv+"="+readyPath)
	target.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := target.Start(); err != nil {
		t.Fatal(err)
	}
	waitForTestFile(t, readyPath)
	identity, err := inspectProcess(target.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := openProcessSignalHandle(identity)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if err := target.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := target.Wait(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", t.TempDir())
	replacement := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, replacement)
	if err := handle.Signal(syscall.SIGKILL); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("expired process handle signal = %v, want ESRCH", err)
	}
	assertGroupAlive(t, replacement.Process.Pid)
}

func TestRecordAcceptsPIDOneOwnerIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, leader)
	path := recordPath(t, leader.Process.Pid)
	rewriteRecord(t, path, func(rec *pgidRecord) {
		rec.Owner.PID = 1
	})
	if _, _, err := readPgidRecord(path); err != nil {
		t.Fatalf("PID 1 owner record was rejected: %v", err)
	}
}

func registryHelperCommand(role string) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestProcessGroupRegistryHelper$")
	command.Env = append(os.Environ(), processGroupRoleEnv+"="+role)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command
}

type registryLeader struct {
	*exec.Cmd
	group *TrackedProcessGroup
}

func startRegistryLeader(t *testing.T, role string) *registryLeader {
	t.Helper()
	readyPath := filepath.Join(t.TempDir(), "ready")
	command := registryHelperCommand(role)
	command.Env = append(command.Env, processGroupReadyFileEnv+"="+readyPath)
	group, err := StartTrackedProcessGroup(command)
	if err != nil {
		t.Fatal(err)
	}
	waitForTestFile(t, readyPath)
	return &registryLeader{Cmd: command, group: group}
}

func stopRegistryLeader(t *testing.T, leader *registryLeader) {
	t.Helper()
	_ = leader.group.Signal(context.Background(), syscall.SIGTERM)
	if err := leader.Wait(); err != nil {
		t.Fatal(err)
	}
	_ = leader.group.RemoveIfDead()
}

func spawnOrphanedGroup(t *testing.T, leaderless bool) (int, string) {
	t.Helper()
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pid")
	readyPath := filepath.Join(dir, "ready")
	role := "owner"
	owner := registryHelperCommand(role)
	owner.Env = append(owner.Env,
		processGroupPIDFileEnv+"="+pidPath,
		processGroupReadyFileEnv+"="+readyPath)
	if leaderless {
		role = "leaderless-owner"
		leaderReadyPath := filepath.Join(dir, "leader-ready")
		releasePath := filepath.Join(dir, "release")
		owner = registryHelperCommand(role)
		owner.Env = append(owner.Env,
			processGroupPIDFileEnv+"="+pidPath,
			processGroupReadyFileEnv+"="+readyPath,
			processGroupLeaderReadyEnv+"="+leaderReadyPath,
			processGroupReleaseFileEnv+"="+releasePath)
	}
	if err := owner.Run(); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	waitForTestFile(t, readyPath)
	return pid, recordPath(t, pid)
}

func cleanupOrphanedGroup(pid int, path string) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = os.Remove(path)
}

func recordPath(t *testing.T, pid int) string {
	t.Helper()
	path, err := pgidFilePath(pid)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func rewriteRecord(t *testing.T, path string, mutate func(*pgidRecord)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec pgidRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	mutate(&rec)
	data, err = json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitForTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func waitForTerminationSignal() {
	stopping := make(chan os.Signal, 1)
	signal.Notify(stopping, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(stopping)
	<-stopping
}

func assertGroupAlive(t *testing.T, pgid int) {
	t.Helper()
	if err := syscall.Kill(-pgid, 0); err != nil {
		t.Fatalf("process group %d is not alive: %v", pgid, err)
	}
}

func assertGroupDead(t *testing.T, pgid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(fmt.Sprintf("process group %d is still alive", pgid))
}
