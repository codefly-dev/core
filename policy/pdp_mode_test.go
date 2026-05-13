package policy_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/policy/testharness"
)

func TestResolvePDPMode_Empty_DefaultsOff(t *testing.T) {
	t.Setenv(policy.EnvPDPMode, "")
	m, err := policy.ResolvePDPMode()
	require.NoError(t, err)
	require.Equal(t, policy.PDPModeOff, m)
}

func TestResolvePDPMode_KnownValues(t *testing.T) {
	for _, want := range []policy.PDPMode{policy.PDPModeOff, policy.PDPModeShadow, policy.PDPModeEnforce} {
		t.Run(string(want), func(t *testing.T) {
			t.Setenv(policy.EnvPDPMode, string(want))
			m, err := policy.ResolvePDPMode()
			require.NoError(t, err)
			require.Equal(t, want, m)
		})
	}
}

func TestResolvePDPMode_CaseInsensitive(t *testing.T) {
	t.Setenv(policy.EnvPDPMode, "ENFORCE")
	m, err := policy.ResolvePDPMode()
	require.NoError(t, err)
	require.Equal(t, policy.PDPModeEnforce, m)
}

func TestResolvePDPMode_Typo_FailsLoud(t *testing.T) {
	t.Setenv(policy.EnvPDPMode, "enfocre") // common typo
	_, err := policy.ResolvePDPMode()
	require.Error(t, err,
		"silent default on typo would silently disable enforcement — must fail at startup")
}

func TestResolveRequireManifest(t *testing.T) {
	tests := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"true", true},
		{"True", true},
		{"1", true},
		{"yes", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"banana", false},
	}
	for _, tc := range tests {
		t.Run(tc.val, func(t *testing.T) {
			t.Setenv(policy.EnvPDPRequireManifest, tc.val)
			require.Equal(t, tc.want, policy.ResolveRequireManifest())
		})
	}
}

func TestBuildPDP_Off_Always_Allows(t *testing.T) {
	inner := testharness.NewFakeDeny()
	pdp := policy.BuildPDP(policy.PDPModeOff, inner, policy.PermissionPolicy{}, false, nil)
	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{Tool: "anything"})
	require.True(t, d.Allow,
		"mode=off short-circuits to AllowAllPDP regardless of inner")
	require.Equal(t, 0, inner.CallCount(),
		"off mode must not consult inner — that defeats 'off'")
}

func TestBuildPDP_Shadow_LogsButAllows(t *testing.T) {
	inner := testharness.NewFakeDeny()
	manifest := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{{Action: "x.y", Resource: "*", Reason: "r"}},
	}
	metrics := &policy.PDPMetrics{}
	pdp := policy.BuildPDP(policy.PDPModeShadow, inner, manifest, false, metrics)

	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Tool: "x.y", Args: map[string]any{"resource": "anything"},
	})
	require.True(t, d.Allow, "shadow always allows")
	require.Equal(t, 1, inner.CallCount(), "inner consulted (manifest passed)")
	require.Equal(t, int64(1), metrics.Snapshot().DeniesTotal,
		"shadow recorded the inner deny in metrics for ops dashboards")
}

func TestBuildPDP_Enforce_DeniesWhenInnerDenies(t *testing.T) {
	inner := testharness.NewFakeDeny()
	manifest := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{{Action: "x.y", Resource: "*", Reason: "r"}},
	}
	pdp := policy.BuildPDP(policy.PDPModeEnforce, inner, manifest, false, nil)

	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Tool: "x.y", Args: map[string]any{"resource": "anything"},
	})
	require.False(t, d.Allow, "enforce honors deny")
}

func TestBuildPDP_Enforce_DeniesUndeclaredAction(t *testing.T) {
	inner := testharness.NewFakeAllow() // would allow if asked
	manifest := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{{Action: "github.read_pr", Reason: "r"}},
	}
	pdp := policy.BuildPDP(policy.PDPModeEnforce, inner, manifest, false, nil)

	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{Tool: "github.merge_pr"})
	require.False(t, d.Allow,
		"manifest ceiling denies undeclared action; inner never consulted")
	require.Contains(t, d.Reason, "manifest-ceiling")
	require.Equal(t, 0, inner.CallCount())
}
