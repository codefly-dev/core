package network

import (
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
	// Port must live in [20000, 29900) and be a multiple of 10 so
	// CLIRestPort at +1 is a recognizable companion.
	for _, n := range []string{"a", "b", "c", "mind-server", "some-other-workspace-name"} {
		p := CLIServerPort(n)
		if p < 20000 || p >= 29910 {
			t.Errorf("CLIServerPort(%q) = %d, out of range [20000, 29910)", n, p)
		}
		if p%10 != 0 {
			t.Errorf("CLIServerPort(%q) = %d, not multiple of 10", n, p)
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
