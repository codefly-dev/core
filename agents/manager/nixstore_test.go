package manager

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
)

// ─── NewNixStoreFromEnv ───────────────────────────────────

// TestNewNixStoreFromEnv_UnsetReturnsNil locks the contract the loader
// relies on: no AGENT_NIX_FLAKE env var means the NixStore is silently
// skipped and the loader falls through to OCIStore / GitHub.
func TestNewNixStoreFromEnv_UnsetReturnsNil(t *testing.T) {
	t.Setenv("AGENT_NIX_FLAKE", "")
	if got := NewNixStoreFromEnv(slog.Default()); got != nil {
		t.Errorf("expected nil when AGENT_NIX_FLAKE unset, got %v", got)
	}
}

// TestNewNixStoreFromEnv_SetReturnsStore covers the happy path. We skip
// the `nix` LookPath check by requiring nix is installed — otherwise the
// env-driven constructor returns nil and we can't verify the ref.
func TestNewNixStoreFromEnv_SetReturnsStore(t *testing.T) {
	if _, err := lookPath("nix"); err != nil {
		t.Fatal("nix not installed; install Nix or run with -tags skip_infra to exclude")
	}
	t.Setenv("AGENT_NIX_FLAKE", "github:acme/flake/v1")
	store := NewNixStoreFromEnv(slog.Default())
	if store == nil {
		t.Fatal("expected non-nil store when AGENT_NIX_FLAKE set and nix in PATH")
	}
	if store.flakeRef != "github:acme/flake/v1" {
		t.Errorf("flakeRef: got %q", store.flakeRef)
	}
}

// ─── attrFor / fullRef ────────────────────────────────────

// TestNixStore_AttrFor locks the Nix attribute naming contract. This
// MUST match the path users/publishers put in their flake's
// `packages.${system}.<attr>`. Change the format, every plugin flake
// breaks.
func TestNixStore_AttrFor(t *testing.T) {
	s := NewNixStore("noop", nil)
	cases := []struct {
		agent *resources.Agent
		want  string
	}{
		{
			agent: &resources.Agent{Kind: "codefly:service", Name: "go-grpc", Version: "0.0.147"},
			want:  "agents-service-go-grpc-0.0.147",
		},
		{
			agent: &resources.Agent{Kind: "codefly:module", Name: "user-management", Version: "1.2.3"},
			want:  "agents-module-user-management-1.2.3",
		},
	}
	for _, tc := range cases {
		got, err := s.attrFor(tc.agent)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("attrFor(%+v) = %q, want %q", tc.agent, got, tc.want)
		}
	}
	if _, err := s.attrFor(&resources.Agent{Kind: "service", Name: "bare", Version: "0.1.0"}); err == nil {
		t.Fatal("unregistered kind must fail closed")
	}
}

// TestNixStore_FullRef verifies the flake#attribute composition that
// Pull and Available pass to `nix build` / `nix eval`.
func TestNixStore_FullRef(t *testing.T) {
	s := NewNixStore("github:codefly-dev/codefly/v0.5", nil)
	agent := &resources.Agent{Kind: "codefly:service", Name: "go-grpc", Version: "0.0.147"}
	want := "github:codefly-dev/codefly/v0.5#agents-service-go-grpc-0.0.147"
	got, err := s.fullRef(agent)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("fullRef = %q, want %q", got, want)
	}
}

// ─── resolveNixAgentBinary ────────────────────────────────

// TestResolveNixAgentBinary_FileOutPath covers the single-binary
// convention: if the derivation's out path is a file, it IS the binary.
func TestResolveNixAgentBinary_FileOutPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "service-single")
	mustWriteExec(t, bin)

	agent := &resources.Agent{Kind: "codefly:service", Name: "single"}
	got, err := resolveNixAgentBinary(bin, agent)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != bin {
		t.Errorf("single-file out path: got %q, want %q", got, bin)
	}
}

// TestResolveNixAgentBinary_BinDir_ConventionallyNamed covers the
// nixpkgs Go package convention: output is a directory with
// bin/service-<name>.
func TestResolveNixAgentBinary_BinDir_ConventionallyNamed(t *testing.T) {
	out := t.TempDir()
	binDir := filepath.Join(out, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "service-go-grpc")
	mustWriteExec(t, bin)
	// Decoy — make sure we match on name, not on first-listed.
	mustWriteExec(t, filepath.Join(binDir, "aaa-decoy"))

	agent := &resources.Agent{Kind: "codefly:service", Name: "go-grpc"}
	got, err := resolveNixAgentBinary(out, agent)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != bin {
		t.Errorf("got %q, want %q (should match service-<name>)", got, bin)
	}
}

// TestResolveNixAgentBinary_BinDir_FallbackToFirst covers the case where
// a plugin ships a binary with a non-standard name.
func TestResolveNixAgentBinary_BinDir_FallbackToFirst(t *testing.T) {
	out := t.TempDir()
	binDir := filepath.Join(out, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "alpha-bin") // only entry, name doesn't match
	mustWriteExec(t, bin)

	agent := &resources.Agent{Kind: "codefly:service", Name: "mismatch"}
	got, err := resolveNixAgentBinary(out, agent)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != bin {
		t.Errorf("fallback: got %q, want %q", got, bin)
	}
}

func TestResolveNixAgentBinary_VerifiedArtifactRequiresExpectedName(t *testing.T) {
	out := t.TempDir()
	binDir := filepath.Join(out, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteExec(t, filepath.Join(binDir, "different-provider"))

	agent := &resources.Agent{Kind: resources.ProviderAgent, Publisher: "codefly.dev", Name: "fixture", Version: "1.2.3"}
	_, err := resolveNixAgentBinary(out, agent)
	if err == nil || !strings.Contains(err.Error(), "missing expected executable") {
		t.Fatalf("expected missing executable error, got %v", err)
	}
}

func TestResolveNixAgentBinary_VerifiedArtifactUsesDescriptorBoundBinary(t *testing.T) {
	out := buildProviderFixture(t)
	got, err := resolveNixAgentBinary(out, providerFixtureAgent())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(out, "provider-fixture")
	if got != want {
		t.Fatalf("got %q, want descriptor-bound binary %q", got, want)
	}
}

// TestResolveNixAgentBinary_EmptyBinDir returns a helpful error rather
// than a confusing downstream "exec format error" from the loader.
func TestResolveNixAgentBinary_EmptyBinDir(t *testing.T) {
	out := t.TempDir()
	if err := os.MkdirAll(filepath.Join(out, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := &resources.Agent{Kind: "codefly:service", Name: "empty"}
	_, err := resolveNixAgentBinary(out, agent)
	if err == nil {
		t.Fatal("expected error for empty bin dir")
	}
}

// TestResolveNixAgentBinary_NoBinDir errors clearly when the directory
// has neither bin/ nor contains a binary directly.
func TestResolveNixAgentBinary_NoBinDir(t *testing.T) {
	out := t.TempDir()
	// Put a non-bin dir there so it's a directory but has no bin/.
	if err := os.MkdirAll(filepath.Join(out, "share"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := &resources.Agent{Kind: "codefly:service", Name: "nobin"}
	_, err := resolveNixAgentBinary(out, agent)
	if err == nil {
		t.Fatal("expected error when bin/ is missing")
	}
}

// TestResolveNixAgentBinary_NonexistentPath surfaces the Stat error path.
func TestResolveNixAgentBinary_NonexistentPath(t *testing.T) {
	agent := &resources.Agent{Kind: "codefly:service", Name: "gone"}
	_, err := resolveNixAgentBinary(filepath.Join(t.TempDir(), "does-not-exist"), agent)
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

// ─── Available (integration — skips without nix) ──────────

// TestNixStore_Available_NoFlake exercises the Available → `nix eval`
// path. A bad ref returns false; no Go-side error bubbles up. Skipped
// if nix isn't installed.
func TestNixStore_Available_UnresolvableRefReturnsFalse(t *testing.T) {
	if _, err := lookPath("nix"); err != nil {
		t.Fatal("nix not installed; install Nix or run with -tags skip_infra to exclude")
	}
	s := NewNixStore("/tmp/does-not-exist-flake", slog.Default())
	agent := &resources.Agent{Kind: "codefly:service", Name: "nonexistent", Version: "0.0.0"}
	ok, err := s.Available(context.Background(), agent)
	if err != nil {
		t.Fatalf("Available should not return an error for unresolvable refs, got %v", err)
	}
	if ok {
		t.Error("expected Available=false for a flake ref that can't be resolved")
	}
}

// ─── helpers ──────────────────────────────────────────────

func mustWriteExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// lookPath is a thin shim around exec.LookPath used only by the skip
// guards in this test file. Declared as a var so it can be overridden
// by future tests that want to force a branch without a real nix on PATH.
var lookPath = exec.LookPath
