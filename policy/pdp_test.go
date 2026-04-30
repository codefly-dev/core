package policy_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
)

func TestAllowAllPDP_AllowsEverything(t *testing.T) {
	d := policy.AllowAllPDP{}.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.status",
	})
	require.True(t, d.Allow)
}

func TestDenyAllPDP_DeniesWithReason(t *testing.T) {
	d := policy.DenyAllPDP{}.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.status",
	})
	require.False(t, d.Allow)
	require.NotEmpty(t, d.Reason, "DenyAll must surface a reason; silent deny is a UX bug")
}

func TestJSONPDP_FirstMatchWins(t *testing.T) {
	pdp := policy.NewJSONPDP(policy.JSONPolicy{
		Default: "deny",
		Rules: []policy.PolicyRule{
			{Toolbox: "git", Tool: "git.status", Allow: true},
			{Toolbox: "git", Allow: false, Reason: "git is mostly off"},
		},
	})

	// First rule matches → allow
	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.status",
	})
	require.True(t, d.Allow,
		"specific allow rule must win over the toolbox-wide deny that follows it")

	// First rule doesn't match (wrong tool); second rule matches → deny
	d = pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.log",
	})
	require.False(t, d.Allow)
	require.Equal(t, "git is mostly off", d.Reason,
		"the matching rule's reason must surface verbatim, not a generic message")
}

func TestJSONPDP_DefaultDeny(t *testing.T) {
	pdp := policy.NewJSONPDP(policy.JSONPolicy{
		Default: "deny",
		Rules: []policy.PolicyRule{
			{Toolbox: "git", Allow: true},
		},
	})

	// No rule matches docker → default-deny applies
	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "docker", Tool: "docker.list_containers",
	})
	require.False(t, d.Allow)
	require.Contains(t, d.Reason, "default-deny",
		"default-deny path must surface that label so operators see why a call was refused")
}

func TestJSONPDP_DefaultAllow(t *testing.T) {
	pdp := policy.NewJSONPDP(policy.JSONPolicy{
		Default: "allow",
		Rules: []policy.PolicyRule{
			{Toolbox: "web", Allow: false, Reason: "no outbound HTTP"},
		},
	})

	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.status",
	})
	require.True(t, d.Allow,
		"unmatched call under default-allow must allow without a reason")

	d = pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "web", Tool: "web.fetch",
	})
	require.False(t, d.Allow,
		"matching deny rule must override default-allow")
	require.Equal(t, "no outbound HTTP", d.Reason)
}

func TestJSONPDP_VerbSuffixMatches(t *testing.T) {
	// "status" alone should match "git.status" — the convenience
	// shorthand for "any-toolbox status command."
	pdp := policy.NewJSONPDP(policy.JSONPolicy{
		Default: "deny",
		Rules: []policy.PolicyRule{
			{Tool: "status", Allow: true},
		},
	})
	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.status",
	})
	require.True(t, d.Allow,
		"verb-only rule \"status\" must match dotted tool \"git.status\"")
}

func TestJSONPDP_FromFile_ReadsAndValidates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")

	// Round-trip: write a real file, load it, exercise it.
	require.NoError(t, os.WriteFile(path, []byte(`{
		"default": "deny",
		"rules": [
			{"toolbox": "git", "allow": true},
			{"toolbox": "docker", "tool": "list_containers", "allow": true}
		]
	}`), 0o600))

	pdp, err := policy.NewJSONPDPFromFile(path)
	require.NoError(t, err)

	require.True(t, pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.status",
	}).Allow)
	require.True(t, pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "docker", Tool: "docker.list_containers",
	}).Allow)
	require.False(t, pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "web", Tool: "web.fetch",
	}).Allow)
}

func TestJSONPDP_FromFile_RejectsBadDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"default": "maybe"}`), 0o600))

	_, err := policy.NewJSONPDPFromFile(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "default must be",
		"a typo'd default must fail loud at load time, not silently behave like deny")
}
