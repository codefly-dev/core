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
		leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := leader.Start(); err != nil {
			t.Fatal(err)
		}
		pid := leader.Process.Pid
		if err := WritePgidFile(pid, "/private/workspace", []string{os.Args[0], "secret"}); err != nil {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			t.Fatal(err)
		}
		writeTestFile(t, os.Getenv(processGroupPIDFileEnv), strconv.Itoa(pid))
		waitForTestFile(t, os.Getenv(processGroupReadyFileEnv))
	case "leaderless-owner":
		leader := registryHelperCommand("leader-with-member")
		leader.Env = append(leader.Env,
			processGroupReadyFileEnv+"="+os.Getenv(processGroupReadyFileEnv),
			processGroupLeaderReadyEnv+"="+os.Getenv(processGroupLeaderReadyEnv),
			processGroupReleaseFileEnv+"="+os.Getenv(processGroupReleaseFileEnv))
		leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := leader.Start(); err != nil {
			t.Fatal(err)
		}
		pid := leader.Process.Pid
		waitForTestFile(t, os.Getenv(processGroupLeaderReadyEnv))
		if err := WritePgidFile(pid, "/private/workspace", []string{os.Args[0], "secret"}); err != nil {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			t.Fatal(err)
		}
		writeTestFile(t, os.Getenv(processGroupPIDFileEnv), strconv.Itoa(pid))
		writeTestFile(t, os.Getenv(processGroupReleaseFileEnv), "release")
		if err := leader.Wait(); err != nil {
			t.Fatal(err)
		}
	case "leader-with-member":
		time.Sleep(50 * time.Millisecond)
		member := registryHelperCommand("member")
		member.Env = append(member.Env, processGroupReadyFileEnv+"="+os.Getenv(processGroupReadyFileEnv))
		if err := member.Start(); err != nil {
			t.Fatal(err)
		}
		waitForTestFile(t, os.Getenv(processGroupReadyFileEnv))
		writeTestFile(t, os.Getenv(processGroupLeaderReadyEnv), "ready")
		waitForTestFile(t, os.Getenv(processGroupReleaseFileEnv))
	}
}

func TestReaperPreservesGroupOwnedByLiveProcess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, leader)
	pid := leader.Process.Pid
	if err := WritePgidFile(pid, t.TempDir(), []string{os.Args[0]}); err != nil {
		t.Fatal(err)
	}

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
	if err := WritePgidFile(pid, t.TempDir(), []string{os.Args[0]}); err != nil {
		t.Fatal(err)
	}
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
		rec.Registered.StartID = rec.Leader.StartID
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
	if err := WritePgidFile(pid, t.TempDir(), []string{os.Args[0]}); err != nil {
		t.Fatal(err)
	}
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

func TestReaperRetainsPartialRecordAndFailsClosed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, leader)
	pid := leader.Process.Pid
	if err := WritePgidFile(pid, t.TempDir(), []string{os.Args[0]}); err != nil {
		t.Fatal(err)
	}
	path := recordPath(t, pid)
	if err := os.WriteFile(path, []byte(`{"pgid":`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ReapStaleProcessGroups(context.Background()); err == nil {
		t.Fatal("reaper accepted a partial record")
	}
	assertGroupAlive(t, pid)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("partial record was removed: %v", err)
	}
}

func TestChangedRecordIsRetained(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, leader)
	pid := leader.Process.Pid
	if err := WritePgidFile(pid, t.TempDir(), []string{os.Args[0]}); err != nil {
		t.Fatal(err)
	}
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

func TestReaperContinuesAfterIndependentRecordFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	failedPID, failedPath := spawnOrphanedGroup(t, false)
	defer cleanupOrphanedGroup(failedPID, failedPath)
	reapedPID, reapedPath := spawnOrphanedGroup(t, false)
	defer cleanupOrphanedGroup(reapedPID, reapedPath)
	if err := os.WriteFile(failedPath, []byte(`{"pgid":`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ReapStaleProcessGroups(context.Background()); err == nil {
		t.Fatal("reaper did not report the invalid record")
	}
	assertGroupAlive(t, failedPID)
	assertGroupDead(t, reapedPID)
}

func TestReaperCancellationDoesNotEscalateToSIGKILL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	leader := startRegistryLeader(t, "ignores-term")
	pid := leader.Process.Pid
	defer func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = leader.Wait()
	}()
	if err := WritePgidFile(pid, t.TempDir(), []string{os.Args[0]}); err != nil {
		t.Fatal(err)
	}
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

func registryHelperCommand(role string) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestProcessGroupRegistryHelper$")
	command.Env = append(os.Environ(), processGroupRoleEnv+"="+role)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command
}

func startRegistryLeader(t *testing.T, role string) *exec.Cmd {
	t.Helper()
	readyPath := filepath.Join(t.TempDir(), "ready")
	command := registryHelperCommand(role)
	command.Env = append(command.Env, processGroupReadyFileEnv+"="+readyPath)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitForTestFile(t, readyPath)
	return command
}

func stopRegistryLeader(t *testing.T, leader *exec.Cmd) {
	t.Helper()
	pid := leader.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	if err := leader.Wait(); err != nil {
		t.Fatal(err)
	}
	_ = RemovePgidFile(pid)
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
