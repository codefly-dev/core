package base

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
