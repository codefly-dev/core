package base

import (
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
	if err := writePgidFile(424242, "/workspace", []string{"redis-server", "--requirepass", secret}); err != nil {
		t.Fatalf("writePgidFile: %v", err)
	}
	path := filepath.Join(os.Getenv("HOME"), ".codefly", pgidDirName, "424242.pgid")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pgid file: %v", err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "--requirepass") {
		t.Fatalf("pgid file leaked argv: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat pgid file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("pgid permissions = %o, want 600", info.Mode().Perm())
	}
}
