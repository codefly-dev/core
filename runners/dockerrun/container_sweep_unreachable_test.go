package dockerrun

import (
	"path/filepath"
	"testing"
)

// An installed Docker client with no daemon is a normal host state for Local
// and Nix runs. Exercise the real SDK against a genuinely absent Unix socket;
// startup cleanup must stay a silent no-op rather than fail the run's sweep.
func TestReapStaleContainersIgnoresUnreachableDaemon(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing-docker.sock"))
	scope, err := NewContainerRecoveryScope(t.TempDir(), t.TempDir(), "offline")
	if err != nil {
		t.Fatal(err)
	}
	if err := ReapStaleContainers(t.Context(), scope); err != nil {
		t.Fatalf("ReapStaleContainers with unreachable daemon: %v", err)
	}
}
