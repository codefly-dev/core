package network

import (
	"fmt"
	"os"
	"testing"
)

func TestCLIServerPort_DeterministicPerWorkspace(t *testing.T) {
	// Same workspace → same port, always. This is the Postman contract.
	first := CLIServerPort("mind-server")
	for range 100 {
		p := CLIServerPort("mind-server")
		if p != first {
			t.Fatalf("CLIServerPort drifted: first=%d now=%d", first, p)
		}
	}
}

func TestCLIServerPort_DifferentWorkspacesDifferentPorts(t *testing.T) {
	// Pick a handful of common names. We don't require NO collisions
	// ever (hash birthday paradox), just that these specific names
	// don't collide.
	names := []string{
		"mind-server",
		"codefly-agents",
		"demo-project",
		"saas-starter",
		"bench-runner",
	}
	seen := make(map[uint16]string)
	for _, n := range names {
		p := CLIServerPort(n)
		if other, ok := seen[p]; ok {
			t.Errorf("CLIServerPort collision: %q and %q both get %d", n, other, p)
		}
		seen[p] = n
	}
}

func TestCLIServerPort_InRange(t *testing.T) {
	// Port must live in [20000, 29900) and be even so CLIRestPort at +1
	// (odd) can never collide with another workspace's gRPC port.
	for _, n := range []string{"a", "b", "c", "mind-server", "some-other-workspace-name"} {
		p := CLIServerPort(n)
		if p < 20000 || p >= 29900 {
			t.Errorf("CLIServerPort(%q) = %d, out of range [20000, 29900)", n, p)
		}
		if p%2 != 0 {
			t.Errorf("CLIServerPort(%q) = %d, not even", n, p)
		}
	}
}

func TestCLIServerPort_EnvOverride(t *testing.T) {
	defer os.Unsetenv("CODEFLY_CLI_SERVER_PORT")

	os.Setenv("CODEFLY_CLI_SERVER_PORT", "54321")
	if got := CLIServerPort("mind-server"); got != 54321 {
		t.Errorf("env override: got %d, want 54321", got)
	}

	// Invalid env value is ignored — falls back to derivation.
	os.Setenv("CODEFLY_CLI_SERVER_PORT", "not-a-number")
	derived := CLIServerPort("mind-server")
	os.Unsetenv("CODEFLY_CLI_SERVER_PORT")
	if derived == 54321 || derived == 0 {
		t.Errorf("invalid env should fall back to hash; got %d", derived)
	}
	if derived != CLIServerPort("mind-server") {
		t.Error("derivation should be deterministic across calls")
	}
}

func TestCLIRestPort_IsGrpcPlusOne(t *testing.T) {
	for _, n := range []string{"mind-server", "codefly-agents", "x"} {
		if CLIRestPort(n) != CLIServerPort(n)+1 {
			t.Errorf("CLIRestPort(%q) should be CLIServerPort+1", n)
		}
	}
}

func TestCLIServerPort_GrpcAndRestPoolsDisjoint(t *testing.T) {
	// A gRPC port (even) must never equal any REST port (odd), no matter
	// the workspace — otherwise a second workspace's gRPC server could
	// steal another's REST companion port. Even/odd parity guarantees it.
	grpc := make(map[uint16]bool)
	rest := make(map[uint16]bool)
	for i := range 500 {
		n := fmt.Sprintf("workspace-%d", i)
		grpc[CLIServerPort(n)] = true
		rest[CLIRestPort(n)] = true
	}
	for p := range grpc {
		if rest[p] {
			t.Errorf("gRPC port %d also used as a REST port", p)
		}
	}
}
