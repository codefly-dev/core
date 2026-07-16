package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSweepStaleAgentSocketsUsesPrivateSpawnDirectories(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)

	stale := filepath.Join(root, "codefly-uds-99999999-deadbeef")
	live := filepath.Join(root, fmt.Sprintf("codefly-uds-%d-live", os.Getpid()))
	unrelated := filepath.Join(root, "unrelated-99999999")
	for _, dir := range []string{stale, live, unrelated} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(stale, "agent.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := CountStaleAgentSockets(); got != 1 {
		t.Fatalf("CountStaleAgentSockets() = %d, want 1", got)
	}
	if got := SweepStaleAgentSockets(); got != 1 {
		t.Fatalf("SweepStaleAgentSockets() = %d, want 1", got)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale private directory still exists: %v", err)
	}
	for _, dir := range []string{live, unrelated} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("directory %q should have been preserved: %v", dir, err)
		}
	}
}
