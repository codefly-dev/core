package base

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestReaperReapsLegacyLeaderGroup(t *testing.T) {
	for _, suffix := range []string{".pgid", ".pgid.invalid"} {
		t.Run(suffix, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			pid, authPath := spawnOrphanedGroup(t, false)
			defer cleanupOrphanedGroup(pid, authPath)
			if err := os.Remove(authPath); err != nil {
				t.Fatal(err)
			}
			legacyPath := legacyRecordPath(t, pid, suffix)
			writeLegacyRecord(t, legacyPath, pid, deadPID, time.Now().Unix())

			if err := ReapStaleProcessGroups(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertGroupDead(t, pid)
			if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("reaped legacy record still exists: %v", err)
			}
		})
	}
}

func TestReaperPreservesReusedLegacyGroupWithoutSignaling(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid, authPath := spawnOrphanedGroup(t, false)
	defer cleanupOrphanedGroup(pid, authPath)
	if err := os.Remove(authPath); err != nil {
		t.Fatal(err)
	}
	legacyPath := legacyRecordPath(t, pid, ".pgid.invalid")
	// A spawn second in the distant past cannot belong to a leader that is
	// alive now, so the pgid must be treated as reused and never signaled.
	writeLegacyRecord(t, legacyPath, pid, deadPID, 1)

	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGroupAlive(t, pid)
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("reused legacy record was not retained: %v", err)
	}
}

func TestReaperReapsLegacyGroupWhenOwnerPidReused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid, authPath := spawnOrphanedGroup(t, false)
	defer cleanupOrphanedGroup(pid, authPath)
	if err := os.Remove(authPath); err != nil {
		t.Fatal(err)
	}
	leaderStart, err := processStartUnixSeconds(pid)
	if err != nil {
		t.Fatal(err)
	}
	// Only an owner born beyond the uncertainty window proves PID reuse.
	// Gate on the kernel timestamp used by the reaper, not wall-clock time.
	owner := spawnOwnerAfterSecond(t, leaderStart+legacyStartCorroborationSkew)
	legacyPath := legacyRecordPath(t, pid, ".pgid")
	writeLegacyRecord(t, legacyPath, pid, owner.Process.Pid, leaderStart)

	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGroupDead(t, pid)
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reaped legacy record still exists: %v", err)
	}
}

func TestReaperPreservesLegacyGroupWhoseLeaderReadsPastTheRecord(t *testing.T) {
	for _, seconds := range []int64{1, 2, legacyStartCorroborationSkew, legacyStartCorroborationSkew + 1} {
		t.Run(strconv.FormatInt(seconds, 10), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			pid, authPath := spawnOrphanedGroup(t, false)
			defer cleanupOrphanedGroup(pid, authPath)
			if err := os.Remove(authPath); err != nil {
				t.Fatal(err)
			}
			leaderStart, err := processStartUnixSeconds(pid)
			if err != nil {
				t.Fatal(err)
			}
			legacyPath := legacyRecordPath(t, pid, ".pgid")
			// This is also the state left by a pgid recycled seconds after its
			// old group exited. The record cannot distinguish reuse from drift.
			writeLegacyRecord(t, legacyPath, pid, deadPID, leaderStart-seconds)
			if err := ReapStaleProcessGroups(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertGroupAlive(t, pid)
			if _, err := os.Stat(legacyPath); err != nil {
				t.Fatalf("ambiguous legacy record was not retained: %v", err)
			}
		})
	}
}

func TestLegacyOwnerAlivePreservesUncertainStart(t *testing.T) {
	owner := exec.Command("sleep", "30")
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = owner.Process.Kill()
		_ = owner.Wait()
	}()
	start, err := processStartUnixSeconds(owner.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, drift := range []int64{0, 1, legacyStartCorroborationSkew, legacyStartCorroborationSkew + 1} {
		alive, err := legacyOwnerAlive(owner.Process.Pid, start-drift)
		if err != nil {
			t.Fatal(err)
		}
		if want := drift <= legacyStartCorroborationSkew; alive != want {
			t.Fatalf("owner drift %d: alive=%v, want %v", drift, alive, want)
		}
	}
}

func TestProcessStartUnixSecondsRemainsStable(t *testing.T) {
	before := time.Now().Unix()
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()
	start, err := processStartUnixSeconds(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	// Linux exposes whole boot seconds and whole start ticks separately, so
	// truncation can put a newly forked process one second before the wall clock.
	if start < before-1 || start > time.Now().Unix() {
		t.Fatalf("process start %d is outside its observed birth interval", start)
	}
	// Cross a wall-clock second while reading the same real process birth.
	for range 12 {
		time.Sleep(100 * time.Millisecond)
		observed, err := processStartUnixSeconds(child.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		if observed != start {
			t.Fatalf("unchanged process start moved from %d to %d", start, observed)
		}
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if _, err := processStartUnixSeconds(child.Process.Pid); !errors.Is(err, errProcessNotFound) {
		t.Fatalf("reaped process start returned %v, want process not found", err)
	}
}

func TestReaperPreservesLegacyGroupWithUncertainLiveOwner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pidPath := filepath.Join(t.TempDir(), "pid")
	readyPath := filepath.Join(t.TempDir(), "ready")
	owner := registryHelperCommand("live-owner")
	owner.Env = append(owner.Env, processGroupPIDFileEnv+"="+pidPath, processGroupReadyFileEnv+"="+readyPath)
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = owner.Process.Signal(syscall.SIGTERM)
		_ = owner.Wait()
	}()
	waitForTestFile(t, readyPath)
	waitForTestFile(t, pidPath)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(recordPath(t, pid)); err != nil {
		t.Fatal(err)
	}
	start, err := processStartUnixSeconds(owner.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := legacyRecordPath(t, pid, ".pgid")
	writeLegacyRecord(t, legacyPath, pid, owner.Process.Pid, start-1)
	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGroupAlive(t, pid)
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("live owner's legacy record was not retained: %v", err)
	}
}

func TestReaperSIGKILLsStubbornLegacyLeader(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid := spawnOrphanedStubbornLeader(t)
	defer func() { _ = syscall.Kill(-pid, syscall.SIGKILL) }()
	if err := os.Remove(recordPath(t, pid)); err != nil {
		t.Fatal(err)
	}
	legacyPath := legacyRecordPath(t, pid, ".pgid")
	writeLegacyRecord(t, legacyPath, pid, deadPID, time.Now().Unix())

	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGroupDead(t, pid)
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reaped legacy record still exists: %v", err)
	}
}

func TestReaperPreservesLegacyGroupWithLiveOwner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid, authPath := spawnOrphanedGroup(t, false)
	defer cleanupOrphanedGroup(pid, authPath)
	if err := os.Remove(authPath); err != nil {
		t.Fatal(err)
	}
	legacyPath := legacyRecordPath(t, pid, ".pgid")
	writeLegacyRecord(t, legacyPath, pid, os.Getpid(), time.Now().Unix())

	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertGroupAlive(t, pid)
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("live-owner legacy record was not retained: %v", err)
	}
}

func TestReaperDropsDeadLegacyRecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pid, authPath := spawnOrphanedGroup(t, false)
	if err := os.Remove(authPath); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	assertGroupDead(t, pid)
	legacyPath := legacyRecordPath(t, pid, ".pgid")
	writeLegacyRecord(t, legacyPath, pid, deadPID, time.Now().Unix())

	if err := ReapStaleProcessGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dead legacy record was not dropped: %v", err)
	}
}

func TestParseLegacyProcessRecordRejectsForeignContracts(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"foreign-plaintext": "foreign record contract\n",
		"current-json":      `{"pgid":5446,"leader":{"pid":5446}}` + "\n",
		"extra-key":         "pgid=10\nparent=1\nstarted=2\ncwd=/x\ncmd=go\nextra=1\n",
		"missing-cwd":       "pgid=10\nparent=1\nstarted=2\ncmd=go\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := parseLegacyProcessRecord(path); ok {
			t.Fatalf("%s parsed as a codefly legacy record", name)
		}
	}
}

// deadPID names an owner that is guaranteed not to be running, so the reaper
// treats the legacy group as orphaned.
const deadPID = 0x7fffffff

func spawnOrphanedStubbornLeader(t *testing.T) int {
	t.Helper()
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pid")
	readyPath := filepath.Join(dir, "ready")
	owner := registryHelperCommand("stubborn-owner")
	owner.Env = append(owner.Env,
		processGroupPIDFileEnv+"="+pidPath,
		processGroupReadyFileEnv+"="+readyPath)
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
	return pid
}

// spawnOwnerAfterSecond starts a live helper whose kernel start second is
// strictly after the supplied boundary.
func spawnOwnerAfterSecond(t *testing.T, after int64) *exec.Cmd {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		owner := exec.Command("sleep", "30")
		if err := owner.Start(); err != nil {
			t.Fatal(err)
		}
		start, err := processStartUnixSeconds(owner.Process.Pid)
		if err != nil {
			_ = owner.Process.Kill()
			_ = owner.Wait()
			t.Fatal(err)
		}
		if start > after {
			t.Cleanup(func() {
				_ = owner.Process.Kill()
				_ = owner.Wait()
			})
			return owner
		}
		_ = owner.Process.Kill()
		_ = owner.Wait()
		if time.Now().After(deadline) {
			t.Fatalf("owner process start second %d never advanced past %d", start, after)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func legacyRecordPath(t *testing.T, pgid int, suffix string) string {
	t.Helper()
	dir, err := pgidStateDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(filepath.Dir(dir), fmt.Sprintf("%d%s", pgid, suffix))
}

func writeLegacyRecord(t *testing.T, path string, pgid, parent int, started int64) {
	t.Helper()
	content := fmt.Sprintf("pgid=%d\nparent=%d\nstarted=%d\ncwd=%s\ncmd=go <2 args>\n",
		pgid, parent, started, t.TempDir())
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
