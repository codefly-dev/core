package manager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
)

// TestLoad_NilAgent_ReturnsErrAgentNil locks the contract that callers
// can switch on errors.Is(err, ErrAgentNil) to distinguish programmer
// error from real failures.
func TestLoad_NilAgent_ReturnsErrAgentNil(t *testing.T) {
	_, err := Load(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error from Load(nil)")
	}
	if !errors.Is(err, ErrAgentNil) {
		t.Errorf("err should match ErrAgentNil via errors.Is: %v", err)
	}
}

// TestLoad_NonexistentBinary_ReturnsErrBinaryNotFound covers the
// resolve-failure path. With no AGENT_REGISTRY / AGENT_NIX_FLAKE set
// and a bogus agent ref, the GitHub fallback fails and we should map
// to ErrAgentBinaryNotFound for the CLI to suggest `codefly agent build`.
func TestLoad_NonexistentBinary_ReturnsErrBinaryNotFound(t *testing.T) {
	t.Setenv("AGENT_NIX_FLAKE", "")
	t.Setenv("AGENT_REGISTRY", "")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := Load(ctx, &resources.Agent{
		Kind:      "codefly:service",
		Publisher: "codefly.dev",
		Name:      "definitely-not-a-real-agent-xyz",
		Version:   "9.9.9",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if !errors.Is(err, ErrAgentBinaryNotFound) {
		t.Errorf("err should match ErrAgentBinaryNotFound via errors.Is: %v", err)
	}
}

// TestErrSentinels_AreDistinct asserts every sentinel is its own value.
// Critical for callers using errors.Is — if two sentinels were aliased,
// switch logic would route incorrectly.
func TestErrSentinels_AreDistinct(t *testing.T) {
	all := []error{
		ErrAgentNil,
		ErrAgentBinaryNotFound,
		ErrAgentSpawn,
		ErrAgentHandshakeTimeout,
		ErrAgentHandshakeMalformed,
		ErrAgentVersionMismatch,
		ErrAgentDialTimeout,
		ErrStoreUnavailable,
		ErrStoreArtifactMissing,
	}
	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("sentinels overlap: %v matches %v (would route incorrectly)", a, b)
			}
		}
	}
}
