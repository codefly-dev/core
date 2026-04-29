//go:build integration

package manager

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/codefly-dev/core/resources"
)

// TestOCIStore_PullFromLocalRegistry tests pulling a real agent binary
// from the k3d local registry.
//
// Requires: AGENT_REGISTRY=localhost:5111 and go-generic:0.0.1 pushed.
// Run: AGENT_REGISTRY=localhost:5111 go test ./agents/manager/ -run TestOCIStore_Pull -v
func TestOCIStore_PullFromLocalRegistry(t *testing.T) {
	registry := os.Getenv("AGENT_REGISTRY")
	if registry == "" {
		t.Fatal("AGENT_REGISTRY not set — required for the OCI integration test (e.g. localhost:5111)")
	}

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	store := NewOCIStore(registry, "http", logger)

	agent, err := resources.ParseAgent(ctx, resources.ServiceAgent, "go-generic:0.0.1")
	if err != nil {
		t.Fatalf("parse agent: %v", err)
	}
	agent.Publisher = "codefly.dev"

	// Check availability
	avail, err := store.Available(ctx, agent)
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	if !avail {
		t.Fatal("go-generic:0.0.1 not in registry — push it first")
	}
	t.Log("agent is available in registry")

	// Pull — use a temp dir so we don't pollute the real cache
	tmpDir := t.TempDir()
	os.Setenv("CODEFLY_PATH", tmpDir)
	defer os.Unsetenv("CODEFLY_PATH")

	path, err := store.Pull(ctx, agent)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}

	// Verify the binary exists and is executable
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat pulled binary: %v", err)
	}
	if info.Size() < 1000 {
		t.Errorf("binary too small: %d bytes", info.Size())
	}
	if info.Mode()&0o111 == 0 {
		t.Error("binary is not executable")
	}
	t.Logf("pulled to %s (%d bytes)", path, info.Size())
}
