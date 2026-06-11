package policy_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
)

const yamlAllowSimple = `
tools:
  - id: codefly.dev/test:1.0:tool.x
    allow: true
    ttl: 60s
    max_uses: 1
`

const yamlDenyWithReason = `
tools:
  - id: codefly.dev/test:1.0:tool.dangerous
    deny: true
    reason: "no force pushes ever"
`

const yamlWithBuiltinCaveats = `
tools:
  - id: codefly.dev/test:1.0:tool.gated
    allow: true
    ttl: 30s
    max_uses: 1
    caveats:
      time_window:
        start_hour: 0
        end_hour: 24
      rate_limit:
        per_minute: 5
        scope: principal
      allowlist:
        context_key: ci_status
        allowed: [green, success]
`

func TestParseYAML_Allow_Simple(t *testing.T) {
	policies, err := policy.ParseYAMLToolPolicies([]byte(yamlAllowSimple))
	require.NoError(t, err)
	require.Len(t, policies, 1)
	tp, ok := policies["codefly.dev/test:1.0:tool.x"]
	require.True(t, ok)

	resolved, err := tp.Evaluate(context.Background(), policy.EvaluationInput{
		Principal: &policy.Principal{ID: "u-1", Kind: policy.KindHuman},
	})
	require.NoError(t, err)
	require.Equal(t, 60*time.Second, resolved.TTL)
	require.Equal(t, 1, resolved.MaxUses)
}

func TestParseYAML_Deny_Surfaces_Reason(t *testing.T) {
	policies, err := policy.ParseYAMLToolPolicies([]byte(yamlDenyWithReason))
	require.NoError(t, err)

	tp := policies["codefly.dev/test:1.0:tool.dangerous"]
	require.NotNil(t, tp)
	_, err = tp.Evaluate(context.Background(), policy.EvaluationInput{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no force pushes")
}

func TestParseYAML_BuiltinCaveats_AllowAndProduce(t *testing.T) {
	policies, err := policy.ParseYAMLToolPolicies([]byte(yamlWithBuiltinCaveats))
	require.NoError(t, err)

	tp := policies["codefly.dev/test:1.0:tool.gated"]
	require.NotNil(t, tp)

	in := policy.EvaluationInput{
		Principal: &policy.Principal{ID: "u-yaml-test", Kind: policy.KindHuman},
		Toolbox:   "codefly.dev/test:1.0",
		Tool:      "tool.gated",
		Context:   map[string]any{"ci_status": "green"},
	}
	resolved, err := tp.Evaluate(context.Background(), in)
	require.NoError(t, err, "all caveats accept (24-hour window, ci=green, under rate)")
	require.Equal(t, 30*time.Second, resolved.TTL)
	require.NotEmpty(t, resolved.CaveatProducers, "producers from time_window/allowlist must be present")
	require.NotContains(t, resolved.CaveatProducers, "rate_limit",
		"rate_limit is mint-only — no producer in the resolved policy")
}

func TestParseYAML_AllowlistDenies_WhenContextMismatches(t *testing.T) {
	policies, err := policy.ParseYAMLToolPolicies([]byte(yamlWithBuiltinCaveats))
	require.NoError(t, err)
	tp := policies["codefly.dev/test:1.0:tool.gated"]

	_, err = tp.Evaluate(context.Background(), policy.EvaluationInput{
		Principal: &policy.Principal{ID: "u-yaml-test-2", Kind: policy.KindHuman},
		Toolbox:   "codefly.dev/test:1.0",
		Tool:      "tool.gated",
		Context:   map[string]any{"ci_status": "red"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "red")
}

func TestParseYAML_RateLimitEnforced_AcrossCalls(t *testing.T) {
	const yaml = `
tools:
  - id: codefly.dev/rl:1.0:hot
    allow: true
    caveats:
      rate_limit:
        per_minute: 2
`
	policies, err := policy.ParseYAMLToolPolicies([]byte(yaml))
	require.NoError(t, err)
	tp := policies["codefly.dev/rl:1.0:hot"]

	in := policy.EvaluationInput{
		Principal: &policy.Principal{ID: "u-rl", Kind: policy.KindHuman},
		Toolbox:   "codefly.dev/rl:1.0", Tool: "hot",
	}
	_, err = tp.Evaluate(context.Background(), in)
	require.NoError(t, err)
	_, err = tp.Evaluate(context.Background(), in)
	require.NoError(t, err)
	_, err = tp.Evaluate(context.Background(), in)
	require.Error(t, err, "rate-limit must deny on third mint within minute")
}

func TestParseYAML_BothAllowAndDeny_Rejected(t *testing.T) {
	const yaml = `
tools:
  - id: x:y:z
    allow: true
    deny: true
    reason: r
`
	_, err := policy.ParseYAMLToolPolicies([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "exactly one of allow/deny")
}

func TestParseYAML_NeitherAllowNorDeny_Rejected(t *testing.T) {
	const yaml = `
tools:
  - id: x:y:z
    ttl: 30s
`
	_, err := policy.ParseYAMLToolPolicies([]byte(yaml))
	require.Error(t, err)
}

func TestParseYAML_Deny_WithoutReason_Rejected(t *testing.T) {
	const yaml = `
tools:
  - id: x:y:z
    deny: true
`
	_, err := policy.ParseYAMLToolPolicies([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "reason")
}

func TestParseYAML_DuplicateID_Rejected(t *testing.T) {
	const yaml = `
tools:
  - id: dup:dup:dup
    allow: true
  - id: dup:dup:dup
    allow: true
`
	_, err := policy.ParseYAMLToolPolicies([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate id")
}

func TestParseYAML_BadTTL_Rejected(t *testing.T) {
	const yaml = `
tools:
  - id: x:y:z
    allow: true
    ttl: not-a-duration
`
	_, err := policy.ParseYAMLToolPolicies([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "ttl")
}

func TestParseYAML_NegativeMaxUses_Rejected(t *testing.T) {
	const yaml = `
tools:
  - id: x:y:z
    allow: true
    max_uses: -1
`
	_, err := policy.ParseYAMLToolPolicies([]byte(yaml))
	require.Error(t, err)
}

func TestParseYAML_UnknownCaveat_Rejected(t *testing.T) {
	const yaml = `
tools:
  - id: x:y:z
    allow: true
    caveats:
      not_a_real_caveat:
        foo: bar
`
	_, err := policy.ParseYAMLToolPolicies([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "not_a_real_caveat")
}

func TestParseYAML_BadCaveatSpec_RejectedAtParse(t *testing.T) {
	const yaml = `
tools:
  - id: x:y:z
    allow: true
    caveats:
      time_window:
        start_hour: 99
`
	_, err := policy.ParseYAMLToolPolicies([]byte(yaml))
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

func TestParseYAML_MalformedYAML_Rejected(t *testing.T) {
	_, err := policy.ParseYAMLToolPolicies([]byte("this is not: : valid yaml: : :"))
	require.Error(t, err)
}

func TestParseYAML_Empty_Returns_EmptyMap(t *testing.T) {
	policies, err := policy.ParseYAMLToolPolicies([]byte(""))
	require.NoError(t, err)
	require.Empty(t, policies)
}

func TestParseYAML_WildcardID_PreservedAsKey(t *testing.T) {
	const yaml = `
tools:
  - id: codefly.dev/x:1.0:*
    allow: true
    ttl: 90s
`
	policies, err := policy.ParseYAMLToolPolicies([]byte(yaml))
	require.NoError(t, err)
	_, ok := policies["codefly.dev/x:1.0:*"]
	require.True(t, ok, "wildcard id must round-trip as key for GatewayEvaluator's wildcard lookup")
}

// =====================================================================
// Integration: parsed YAML → GatewayEvaluator → minted token
// =====================================================================

func TestYAML_Integration_ParseThenMint(t *testing.T) {
	const yaml = `
tools:
  - id: codefly.dev/integ:1.0:safe
    allow: true
    ttl: 30s
    caveats:
      time_window:
        start_hour: 0
        end_hour: 24
`
	policies, err := policy.ParseYAMLToolPolicies([]byte(yaml))
	require.NoError(t, err)

	g := &policy.GatewayEvaluator{
		ToolPolicies: policies,
		Decider:      newAllowDecider(),
		Secret:       policy.NewSpawnSecret(),
		DefaultTTL:   60 * time.Second,
	}
	result, err := g.EvaluateAndMint(context.Background(), policy.EvaluationInput{
		Principal: &policy.Principal{ID: "u-integ", Kind: policy.KindHuman, OrgID: "o"},
		Toolbox:   "codefly.dev/integ:1.0",
		Tool:      "safe",
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Token)
	require.NotNil(t, result.Authorization.Caveats["time_window"],
		"YAML-defined time_window must produce a caveat in the minted token")
}

// newAllowDecider is a minimal Decider that always allows; used
// for integration tests where the focus is the policy + caveat
// pipeline, not the role-grant check.
type allowDecider struct{}

func (allowDecider) Evaluate(_ context.Context, _ *policy.PDPRequest) policy.PDPDecision {
	return policy.PDPDecision{Allow: true}
}

func newAllowDecider() policy.Decider { return allowDecider{} }
