package policy_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/policy/testharness"
)

// =====================================================================
// PermissionPolicy unit tests
// =====================================================================

func TestPermissionPolicy_Validate_Required_NeedsReason(t *testing.T) {
	p := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{
			{Action: "github.read_pr", Resource: "repo:foo/*"},
		},
	}
	err := p.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "reason",
		"required permissions must surface a human-readable reason for the install-time UI")
}

func TestPermissionPolicy_Validate_Optional_ReasonOptional(t *testing.T) {
	p := policy.PermissionPolicy{
		Optional: []policy.PermissionDeclaration{
			{Action: "github.deploy_staging", Resource: "env:staging"}, // no reason
		},
	}
	require.NoError(t, p.Validate(), "optional reasons can be empty (less load-bearing in UI)")
}

func TestPermissionPolicy_Validate_EmptyAction_Rejected(t *testing.T) {
	p := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{{Resource: "repo:*", Reason: "x"}},
	}
	require.Error(t, p.Validate())
}

func TestPermissionPolicy_Validate_RejectsUnsupportedGlobShape(t *testing.T) {
	// globMatch only supports bare "*", "prefix*", and exact strings.
	// Other forms used to match nothing silently — Validate now fails
	// them loud at install time. Cover the three rejection shapes.
	cases := []struct {
		name string
		decl policy.PermissionDeclaration
	}{
		{
			name: "leading * in action",
			decl: policy.PermissionDeclaration{Action: "*foo", Reason: "r"},
		},
		{
			name: "mid-string * in action",
			decl: policy.PermissionDeclaration{Action: "foo*bar", Reason: "r"},
		},
		{
			name: "double-star in action",
			decl: policy.PermissionDeclaration{Action: "foo.**", Reason: "r"},
		},
		{
			name: "leading * in resource",
			decl: policy.PermissionDeclaration{Action: "fs.read", Resource: "*secret", Reason: "r"},
		},
		{
			name: "mid-string * in resource",
			decl: policy.PermissionDeclaration{Action: "fs.read", Resource: "repo:*/secret", Reason: "r"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := policy.PermissionPolicy{Required: []policy.PermissionDeclaration{tc.decl}}
			err := p.Validate()
			require.Error(t, err, "Validate should reject unsupported glob shapes")
			require.Contains(t, err.Error(), "*",
				"error should mention the unsupported wildcard")
		})
	}
}

func TestPermissionPolicy_Validate_AcceptsSupportedGlobShape(t *testing.T) {
	// Bare "*", "prefix*", and exact strings must continue to pass.
	cases := []policy.PermissionDeclaration{
		{Action: "*", Reason: "any"},
		{Action: "github.*", Reason: "all github"},
		{Action: "github.read_pr", Reason: "specific"},
		{Action: "fs.read", Resource: "*", Reason: "any resource"},
		{Action: "fs.read", Resource: "repo:codefly-dev/*", Reason: "prefix"},
		{Action: "fs.read", Resource: "", Reason: "no resource constraint"},
	}
	for _, decl := range cases {
		p := policy.PermissionPolicy{Required: []policy.PermissionDeclaration{decl}}
		require.NoError(t, p.Validate(), "supported glob shape was rejected: %+v", decl)
	}
}

func TestPermissionPolicy_Validate_BadRiskLevel_Rejected(t *testing.T) {
	p := policy.PermissionPolicy{
		RiskLevels: map[string]string{"github.merge_pr": "extreme"},
	}
	err := p.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "low|medium|high|critical")
}

func TestPermissionPolicy_Allows_Exact(t *testing.T) {
	p := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{
			{Action: "github.read_pr", Resource: "repo:codefly/x", Reason: "r"},
		},
	}
	require.True(t, p.Allows("github.read_pr", "repo:codefly/x"))
	require.False(t, p.Allows("github.read_pr", "repo:other/x"))
	require.False(t, p.Allows("github.merge_pr", "repo:codefly/x"))
}

func TestPermissionPolicy_Allows_Wildcards(t *testing.T) {
	p := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{
			{Action: "github.*", Resource: "repo:codefly/*", Reason: "all github on our repos"},
		},
	}
	require.True(t, p.Allows("github.read_pr", "repo:codefly/foo"))
	require.True(t, p.Allows("github.merge_pr", "repo:codefly/bar"))
	require.False(t, p.Allows("github.read_pr", "repo:other/foo"),
		"resource glob constrained to codefly org")
	require.False(t, p.Allows("docker.run", "repo:codefly/foo"),
		"action prefix didn't match")
}

func TestPermissionPolicy_Allows_EmptyResource_MatchesAny(t *testing.T) {
	p := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{
			{Action: "fs.read", Resource: "", Reason: "any path"},
		},
	}
	require.True(t, p.Allows("fs.read", "/etc/hosts"))
	require.True(t, p.Allows("fs.read", "anything"))
}

func TestPermissionPolicy_DeclaresAction_IgnoresResourceUntilInvocation(t *testing.T) {
	p := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{{Action: "postgres.query.*", Resource: "database:tenant-a"}},
	}
	require.True(t, p.DeclaresAction("postgres.query.execute"))
	require.False(t, p.DeclaresAction("postgres.admin.drop"))
}

func TestPermissionPolicy_RiskLevelOf_DefaultsLow(t *testing.T) {
	p := policy.PermissionPolicy{
		RiskLevels: map[string]string{
			"github.merge_pr":   policy.RiskLevelMedium,
			"github.force_push": policy.RiskLevelCritical,
		},
	}
	require.Equal(t, policy.RiskLevelMedium, p.RiskLevelOf("github.merge_pr"))
	require.Equal(t, policy.RiskLevelCritical, p.RiskLevelOf("github.force_push"))
	require.Equal(t, policy.RiskLevelLow, p.RiskLevelOf("git.status"),
		"unannotated actions default to low risk")
}

func TestPermissionPolicy_All_PreservesOrder(t *testing.T) {
	p := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{
			{Action: "a.x", Reason: "x"},
			{Action: "a.y", Reason: "y"},
		},
		Optional: []policy.PermissionDeclaration{
			{Action: "a.z"},
		},
	}
	all := p.All()
	require.Len(t, all, 3)
	require.Equal(t, "a.x", all[0].Action)
	require.Equal(t, "a.y", all[1].Action)
	require.Equal(t, "a.z", all[2].Action)
}

func TestPermissionPolicy_IsEmpty(t *testing.T) {
	require.True(t, policy.PermissionPolicy{}.IsEmpty())
	require.False(t, policy.PermissionPolicy{Required: []policy.PermissionDeclaration{{Action: "a", Reason: "r"}}}.IsEmpty())
	require.False(t, policy.PermissionPolicy{Optional: []policy.PermissionDeclaration{{Action: "a"}}}.IsEmpty())
}

// =====================================================================
// CeilingPDP unit tests — the manifest-as-ceiling enforcement
// =====================================================================

func TestCeilingPDP_ManifestAllows_RoleAllows_Allow(t *testing.T) {
	manifest := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{
			{Action: "github.read_pr", Resource: "repo:codefly/*", Reason: "review PRs"},
		},
	}
	inner := testharness.NewFakeAllow()
	pdp := policy.NewCeilingPDP(inner, manifest, false)

	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Tool: "github.read_pr",
		Args: map[string]any{"resource": "repo:codefly/codefly.dev"},
	})
	require.True(t, d.Allow)
	require.Equal(t, 1, inner.CallCount(), "inner consulted when manifest allows")
}

func TestCeilingPDP_ManifestDenies_InnerNeverConsulted(t *testing.T) {
	manifest := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{
			{Action: "github.read_pr", Resource: "repo:codefly/*", Reason: "review only"},
		},
	}
	inner := testharness.NewFakeAllow() // would allow if asked
	pdp := policy.NewCeilingPDP(inner, manifest, false)

	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Tool: "github.merge_pr", // not declared
		Args: map[string]any{"resource": "repo:codefly/codefly.dev"},
	})
	require.False(t, d.Allow,
		"action not in manifest must deny even if inner would allow — that's the whole point of the ceiling")
	require.Contains(t, d.Reason, "manifest-ceiling")
	require.Equal(t, 0, inner.CallCount(),
		"inner must NOT be consulted when manifest denies — saves saas-starter round-trip")
}

func TestCeilingPDP_ManifestAllows_RoleDenies_Deny(t *testing.T) {
	// Both layers must agree — this covers "manifest claims the
	// action but the principal hasn't been granted the role yet".
	manifest := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{
			{Action: "github.merge_pr", Resource: "repo:*", Reason: "auto-merge"},
		},
	}
	inner := testharness.NewFakeDeny()
	pdp := policy.NewCeilingPDP(inner, manifest, false)

	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Tool: "github.merge_pr",
		Args: map[string]any{"resource": "repo:codefly/x"},
	})
	require.False(t, d.Allow)
	require.Contains(t, d.Reason, "fake-pdp", "deny reason from inner is propagated verbatim")
}

func TestCeilingPDP_EmptyManifest_NotRequired_PassThrough(t *testing.T) {
	// During M4 rollout, plugins without manifests pass through.
	pdp := policy.NewCeilingPDP(testharness.NewFakeAllow(), policy.PermissionPolicy{}, false)
	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{Tool: "anything.goes"})
	require.True(t, d.Allow,
		"empty manifest with RequireManifest=false = pass-through (M4 rollout default)")
}

func TestCeilingPDP_EmptyManifest_Required_DeniesAll(t *testing.T) {
	// After the strict-mode flip, plugins MUST declare permissions.
	pdp := policy.NewCeilingPDP(testharness.NewFakeAllow(), policy.PermissionPolicy{}, true)
	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{Tool: "github.read_pr"})
	require.False(t, d.Allow)
	require.Contains(t, d.Reason, "REQUIRE_MANIFEST",
		"strict mode reason must mention the env var so operators can debug")
}

func TestCeilingPDP_NilInner_Panics(t *testing.T) {
	require.Panics(t, func() {
		policy.NewCeilingPDP(nil, policy.PermissionPolicy{}, false)
	})
}

func TestCeilingPDP_NoResourceInArgs_StillEvaluates(t *testing.T) {
	// Some tools have no specific resource (e.g. "git.status"
	// operates on the cwd). Manifest declarations with empty
	// Resource match those.
	manifest := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{
			{Action: "git.status", Resource: "", Reason: "read repo state"},
		},
	}
	inner := testharness.NewFakeAllow()
	pdp := policy.NewCeilingPDP(inner, manifest, false)

	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{Tool: "git.status"})
	require.True(t, d.Allow)
}

// =====================================================================
// Layered defense: Ceiling + Shadow on top of inner
// =====================================================================

func TestPDP_Layered_CeilingThenShadow_BehavesAsExpected(t *testing.T) {
	// Real production stack:
	//   inner = SaasPDP (would deny here because we use FakeDeny)
	//   ceiling = CeilingPDP wrapping inner
	//   shadow = ShadowPDP wrapping ceiling (logs but allows)
	//
	// What ships in M5 enforce mode: just ceiling+inner.
	// What runs in M5 shadow mode: shadow+ceiling+inner.
	manifest := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{
			{Action: "github.read_pr", Resource: "repo:*", Reason: "r"},
		},
	}
	inner := testharness.NewFakeDeny()
	ceiling := policy.NewCeilingPDP(inner, manifest, false)
	shadow := policy.NewShadowPDP(ceiling, nil)

	// Manifest allows + role denies → would be deny → shadow allows.
	d := shadow.Evaluate(context.Background(), &policy.PDPRequest{
		Tool: "github.read_pr",
		Args: map[string]any{"resource": "repo:codefly/x"},
	})
	require.True(t, d.Allow, "shadow always allows")
	require.Contains(t, d.Reason, "would-deny",
		"the inner stack's deny is recorded in shadow's reason")
	require.Equal(t, 1, inner.CallCount(),
		"inner consulted because manifest allowed; shadow recorded the deny but didn't enforce")

	// Action NOT in manifest → ceiling denies; inner still NOT consulted.
	inner.Reset()
	d2 := shadow.Evaluate(context.Background(), &policy.PDPRequest{
		Tool: "github.merge_pr",
		Args: map[string]any{"resource": "repo:codefly/x"},
	})
	require.True(t, d2.Allow, "shadow still allows")
	require.Contains(t, d2.Reason, "manifest-ceiling")
	require.Equal(t, 0, inner.CallCount(),
		"ceiling short-circuits at the manifest layer; inner not touched")
}
