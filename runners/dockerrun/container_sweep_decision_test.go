package dockerrun

import (
	"os"
	"testing"
)

// TestShouldReapContainer pins the orphan-reap decision — including the fix for
// the OrbStack-memory-blowup leak: a RUNNING ephemeral (test) container with a
// dead owner must be reaped, while a running stateful (non-ephemeral) one is
// preserved for reuse.
func TestShouldReapContainer(t *testing.T) {
	cases := []struct {
		name       string
		state      string
		ownerAlive bool
		ephemeral  bool
		want       bool
	}{
		{"owner alive, running, stateful", "running", true, false, false},
		{"owner alive, running, ephemeral", "running", true, true, false},
		{"owner dead, stopped, stateful", "exited", false, false, true},
		{"owner dead, stopped, ephemeral", "exited", false, true, true},
		// The reuse-by-name pattern: a live restart will reuse this — keep.
		{"owner dead, running, stateful (reuse)", "running", false, false, false},
		// THE FIX: a leaked test dependency, running forever with a dead owner.
		{"owner dead, running, ephemeral (LEAK)", "running", false, true, true},
	}
	for _, tc := range cases {
		if got := shouldReapContainer(tc.state, tc.ownerAlive, tc.ephemeral); got != tc.want {
			t.Errorf("%s: shouldReapContainer(%q, alive=%v, ephemeral=%v) = %v, want %v",
				tc.name, tc.state, tc.ownerAlive, tc.ephemeral, got, tc.want)
		}
	}
}

func TestEphemeralContainersFlag(t *testing.T) {
	// Default off.
	SetEphemeralContainers(false)
	if EphemeralContainers() {
		t.Fatal("EphemeralContainers should be false by default")
	}
	SetEphemeralContainers(true)
	if !EphemeralContainers() {
		t.Fatal("SetEphemeralContainers(true) did not take effect")
	}
	if got := os.Getenv(EphemeralContainersEnvironment); got != "1" {
		t.Fatalf("ephemeral process marker = %q, want 1", got)
	}

	ephemeralContainers.Store(false)
	if !EphemeralContainers() {
		t.Fatal("agent process did not inherit the ephemeral environment marker")
	}
	SetEphemeralContainers(false) // reset for other tests
	if _, ok := os.LookupEnv(EphemeralContainersEnvironment); ok {
		t.Fatal("ephemeral process marker was not cleared")
	}
}
