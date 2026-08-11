package base

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandSummaryDoesNotExposeArguments(t *testing.T) {
	summary := CommandSummary([]string{"/usr/bin/redis-server", "--requirepass", "hunter2"})
	if summary != "redis-server <2 args>" || strings.Contains(summary, "hunter2") {
		t.Fatalf("summary = %q", summary)
	}
}

func TestPgidFileIsPrivateAndDoesNotPersistArguments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const secret = "super-secret-password"
	t.Setenv("CODEFLY_TEST_SECRET", secret)
	leader := startRegistryLeader(t, "member")
	defer stopRegistryLeader(t, leader)
	pid := leader.Process.Pid
	path := filepath.Join(os.Getenv("HOME"), ".codefly", pgidDirName, fmt.Sprintf("%d.pgid", pid))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pgid file: %v", err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "--requirepass") || strings.Contains(string(data), "/private/workspace") {
		t.Fatalf("pgid file leaked argv: %s", data)
	}
	if strings.Contains(string(data), `"registered"`) {
		t.Fatalf("pgid file retained wall-clock registration ordering: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat pgid file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("pgid permissions = %o, want 600", info.Mode().Perm())
	}
	rec, _, err := readPgidRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	leaderIdentity, err := inspectProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	ownerIdentity, err := inspectProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if !leaderIdentity.matches(rec.Leader) || !ownerIdentity.matches(rec.Owner) {
		t.Fatal("record did not persist exact process birth identities")
	}
	if len(rec.Authentication) != groupAuthBytes*2 {
		t.Fatalf("authentication length = %d, want %d", len(rec.Authentication), groupAuthBytes*2)
	}
	if rec.Leader.Executable != filepath.Base(os.Args[0]) {
		t.Fatalf("executable identity = %q, want basename %q", rec.Leader.Executable, filepath.Base(os.Args[0]))
	}
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".pgid-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary process-group records remain: %v", temporary)
	}
}
