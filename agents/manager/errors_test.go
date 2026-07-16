package manager

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/sandbox"
)

type admissionSandbox struct{ backend sandbox.Backend }

func (s *admissionSandbox) WithReadPaths(...string) sandbox.Sandbox  { return s }
func (s *admissionSandbox) WithWritePaths(...string) sandbox.Sandbox { return s }
func (s *admissionSandbox) WithNetwork(sandbox.NetworkPolicy) sandbox.Sandbox {
	return s
}
func (s *admissionSandbox) WithUnixSockets(...string) sandbox.Sandbox { return s }
func (s *admissionSandbox) Wrap(*exec.Cmd) error                      { return nil }
func (s *admissionSandbox) Backend() sandbox.Backend                  { return s.backend }

func validAdmissionConfig() loadConfig {
	return loadConfig{
		sandbox:             &admissionSandbox{backend: sandbox.BackendBwrap},
		sandboxChoiceMade:   true,
		principal:           &policy.Principal{ID: "user-1", Kind: policy.KindHuman},
		principalChoiceMade: true,
		permissionsCallback: policy.AllowAllPDP{},
		scopedAuthSecret:    make([]byte, policy.MinScopedAuthSecretBytes),
	}
}

func TestValidateProductionAdmission_RequiresCompleteSecurityEnvelope(t *testing.T) {
	tests := []struct {
		name string
		edit func(*loadConfig)
		want string
	}{
		{name: "sandbox choice", edit: func(c *loadConfig) { c.sandboxChoiceMade = false }, want: "enforcing sandbox"},
		{name: "sandbox value", edit: func(c *loadConfig) { c.sandbox = nil }, want: "enforcing sandbox"},
		{name: "native backend", edit: func(c *loadConfig) { c.sandbox = sandbox.NewNative() }, want: "does not enforce"},
		{name: "principal choice", edit: func(c *loadConfig) { c.principalChoiceMade = false }, want: "principal binding"},
		{name: "principal value", edit: func(c *loadConfig) { c.principal = nil }, want: "principal binding"},
		{name: "expired principal", edit: func(c *loadConfig) { c.principal.ExpiresAt = time.Now().Add(-time.Minute) }, want: "expired"},
		{name: "callback PDP", edit: func(c *loadConfig) { c.permissionsCallback = nil }, want: "callback/PDP"},
		{name: "scoped secret", edit: func(c *loadConfig) { c.scopedAuthSecret = make([]byte, policy.MinScopedAuthSecretBytes-1) }, want: "at least"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validAdmissionConfig()
			tc.edit(&cfg)
			err := validateProductionAdmission(&cfg)
			if !errors.Is(err, ErrAgentAdmission) {
				t.Fatalf("expected ErrAgentAdmission, got %v", err)
			}
			if got := err.Error(); !strings.Contains(got, tc.want) {
				t.Fatalf("error %q does not contain %q", got, tc.want)
			}
		})
	}
}

func TestValidateProductionAdmission_AcceptsCompleteSecurityEnvelope(t *testing.T) {
	cfg := validAdmissionConfig()
	if err := validateProductionAdmission(&cfg); err != nil {
		t.Fatalf("complete production envelope rejected: %v", err)
	}
}

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
	}, WithoutSandbox(), WithoutPrincipal())
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if !errors.Is(err, ErrAgentBinaryNotFound) {
		t.Errorf("err should match ErrAgentBinaryNotFound via errors.Is: %v", err)
	}
}

func TestLoadRequiresExplicitSandboxAndPrincipalChoicesBeforeResolution(t *testing.T) {
	agent := &resources.Agent{Kind: "codefly:service", Publisher: "codefly.dev", Name: "unused", Version: "0.0.1"}
	_, err := Load(context.Background(), agent)
	if !errors.Is(err, ErrAgentAdmission) || !strings.Contains(err.Error(), "WithSandbox") {
		t.Fatalf("missing sandbox choice must fail before resolution: %v", err)
	}

	_, err = Load(context.Background(), agent, WithoutSandbox())
	if !errors.Is(err, ErrAgentAdmission) || !strings.Contains(err.Error(), "WithPrincipal") {
		t.Fatalf("missing principal choice must fail before resolution: %v", err)
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
		ErrAgentAdmission,
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
