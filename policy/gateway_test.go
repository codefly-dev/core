package policy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/policy/testharness"
)

// =====================================================================
// GatewayEvaluator
// =====================================================================

func defaultGatewayInputs() (policy.EvaluationInput, []byte) {
	return policy.EvaluationInput{
		Principal: &policy.Principal{
			ID: "u-antoine", Kind: policy.KindHuman, OrgID: "org-codefly",
		},
		Toolbox:  "codefly.dev/github-bot:0.1.0",
		Tool:     "github.read_pr",
		Resource: "repo:codefly/x",
	}, policy.NewSpawnSecret()
}

func TestGateway_AllowsAndMints_WhenAllPass(t *testing.T) {
	in, secret := defaultGatewayInputs()
	g := &policy.GatewayEvaluator{
		Decider:    testharness.NewFakeAllow(),
		Secret:     secret,
		DefaultTTL: time.Minute,
	}
	result, err := g.EvaluateAndMint(context.Background(), in)
	require.NoError(t, err)
	require.NotNil(t, result.Authorization)
	require.NotEmpty(t, result.Token,
		"on allow, the token must be ready to attach to outgoing metadata")
	require.Contains(t, result.DecisionPath, "tool-policy")
}

func TestGateway_DeniesWhenInnerDeciderDenies(t *testing.T) {
	in, secret := defaultGatewayInputs()
	g := &policy.GatewayEvaluator{
		Decider: testharness.NewFakeDeny(),
		Secret:  secret,
	}
	_, err := g.EvaluateAndMint(context.Background(), in)
	require.ErrorIs(t, err, policy.ErrGatewayDeny,
		"role-grant denial must propagate as ErrGatewayDeny")
}

func TestGateway_DeniesWhenToolPolicyDenies(t *testing.T) {
	in, secret := defaultGatewayInputs()
	g := &policy.GatewayEvaluator{
		Decider:    testharness.NewFakeAllow(), // would allow if asked
		Secret:     secret,
		DefaultTTL: time.Minute,
		ToolPolicies: map[string]policy.ToolPolicy{
			"codefly.dev/github-bot:0.1.0:github.read_pr": policy.DenyAlwaysToolPolicy{
				Reason: "kill-switch active",
			},
		},
	}
	_, err := g.EvaluateAndMint(context.Background(), in)
	require.ErrorIs(t, err, policy.ErrGatewayDeny)
	require.Contains(t, err.Error(), "kill-switch active")
}

func TestGateway_NoPolicy_PassesToInnerDecider(t *testing.T) {
	// When no ToolPolicy is registered, the gateway is a thin
	// wrapper around the inner Decider — manifest-only path.
	in, secret := defaultGatewayInputs()
	deny := testharness.NewFakeDeny()
	g := &policy.GatewayEvaluator{
		Decider:    deny,
		Secret:     secret,
		DefaultTTL: time.Minute,
	}
	_, err := g.EvaluateAndMint(context.Background(), in)
	require.ErrorIs(t, err, policy.ErrGatewayDeny)
	require.Equal(t, 1, deny.CallCount(),
		"inner Decider must be consulted when no tool policy denies first")
}

func TestGateway_WildcardToolPolicy_Matches(t *testing.T) {
	in, secret := defaultGatewayInputs()
	g := &policy.GatewayEvaluator{
		Decider:    testharness.NewFakeAllow(),
		Secret:     secret,
		DefaultTTL: time.Minute,
		ToolPolicies: map[string]policy.ToolPolicy{
			"codefly.dev/github-bot:0.1.0:*": policy.DenyAlwaysToolPolicy{
				Reason: "wildcard kill-switch",
			},
		},
	}
	_, err := g.EvaluateAndMint(context.Background(), in)
	require.ErrorIs(t, err, policy.ErrGatewayDeny,
		"wildcard tool policy applies to all tools in the toolbox")
}

func TestGateway_ExactPolicyBeatsWildcard(t *testing.T) {
	// Exact-key match wins over wildcard. Operator can declare a
	// permissive default at toolbox:* and exceptions at toolbox:tool.
	in, secret := defaultGatewayInputs()
	g := &policy.GatewayEvaluator{
		Decider:    testharness.NewFakeAllow(),
		Secret:     secret,
		DefaultTTL: time.Minute,
		ToolPolicies: map[string]policy.ToolPolicy{
			"codefly.dev/github-bot:0.1.0:*": policy.DenyAlwaysToolPolicy{
				Reason: "wildcard would deny",
			},
			"codefly.dev/github-bot:0.1.0:github.read_pr": policy.AllowAlwaysToolPolicy{
				TTL: time.Minute,
			},
		},
	}
	result, err := g.EvaluateAndMint(context.Background(), in)
	require.NoError(t, err, "exact match must win over wildcard")
	require.NotNil(t, result)
}

func TestGateway_ManifestCeilingPolicy_DeniesUndeclared(t *testing.T) {
	in, secret := defaultGatewayInputs()
	manifest := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{
			{Action: "github.read_pr", Resource: "repo:codefly/*", Reason: "review"},
		},
	}
	g := &policy.GatewayEvaluator{
		Decider:    testharness.NewFakeAllow(),
		Secret:     secret,
		DefaultTTL: time.Minute,
		ToolPolicies: map[string]policy.ToolPolicy{
			"codefly.dev/github-bot:0.1.0:*": policy.ManifestCeilingPolicy{
				Manifest: manifest,
				TTL:      time.Minute,
			},
		},
	}

	// Declared action — passes.
	_, err := g.EvaluateAndMint(context.Background(), in)
	require.NoError(t, err)

	// Undeclared action — denies.
	in.Tool = "github.merge_pr"
	_, err = g.EvaluateAndMint(context.Background(), in)
	require.ErrorIs(t, err, policy.ErrGatewayDeny)
	require.Contains(t, err.Error(), "manifest does not declare")
}

func TestGateway_NilDecider_Errors(t *testing.T) {
	g := &policy.GatewayEvaluator{Secret: policy.NewSpawnSecret()}
	in, _ := defaultGatewayInputs()
	_, err := g.EvaluateAndMint(context.Background(), in)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil Decider")
}

func TestGateway_NilPrincipal_Errors(t *testing.T) {
	g := &policy.GatewayEvaluator{
		Decider: testharness.NewFakeAllow(),
		Secret:  policy.NewSpawnSecret(),
	}
	_, err := g.EvaluateAndMint(context.Background(), policy.EvaluationInput{
		Tool: "x.y",
	})
	require.ErrorIs(t, err, policy.ErrGatewayDeny)
}

func TestGateway_EmptyTool_Errors(t *testing.T) {
	g := &policy.GatewayEvaluator{
		Decider: testharness.NewFakeAllow(),
		Secret:  policy.NewSpawnSecret(),
	}
	_, err := g.EvaluateAndMint(context.Background(), policy.EvaluationInput{
		Principal: &policy.Principal{ID: "p", Kind: policy.KindHuman},
	})
	require.ErrorIs(t, err, policy.ErrGatewayDeny)
}

func TestGateway_TokenIsVerifiable(t *testing.T) {
	// End-to-end: mint at gateway, verify with same secret →
	// must succeed.
	in, secret := defaultGatewayInputs()
	g := &policy.GatewayEvaluator{
		Decider:    testharness.NewFakeAllow(),
		Secret:     secret,
		DefaultTTL: time.Minute,
	}
	result, err := g.EvaluateAndMint(context.Background(), in)
	require.NoError(t, err)

	sa, err := policy.Verify(result.Token, policy.VerifyExpectations{
		Action:      in.Tool,
		Resource:    in.Resource,
		Audience:    in.Toolbox,
		PrincipalID: in.Principal.ID,
	}, secret)
	require.NoError(t, err, "gateway-minted token must verify with the same secret")
	require.Equal(t, in.Tool, sa.Action)
	require.Equal(t, in.Toolbox, sa.AudienceID)
}

func TestGateway_DefaultTTL_AppliedWhenPolicyOmits(t *testing.T) {
	in, secret := defaultGatewayInputs()
	g := &policy.GatewayEvaluator{
		Decider:    testharness.NewFakeAllow(),
		Secret:     secret,
		DefaultTTL: 90 * time.Second,
		ToolPolicies: map[string]policy.ToolPolicy{
			"codefly.dev/github-bot:0.1.0:github.read_pr": policy.AllowAlwaysToolPolicy{
				// TTL not set → DefaultTTL applies.
			},
		},
	}
	result, err := g.EvaluateAndMint(context.Background(), in)
	require.NoError(t, err)

	sa := result.Authorization
	ttl := time.Unix(sa.ExpiresAtUnix, 0).Sub(time.Unix(sa.IssuedAtUnix, 0))
	require.InDelta(t, 90*time.Second, ttl, float64(2*time.Second),
		"DefaultTTL must apply when tool policy omits it")
}

// =====================================================================
// CaveatProducers
// =====================================================================

type caveatProducerPolicy struct {
	producers map[string]policy.CaveatProducer
	ttl       time.Duration
}

func (p caveatProducerPolicy) Evaluate(_ context.Context, _ policy.EvaluationInput) (policy.ResolvedToolPolicy, error) {
	return policy.ResolvedToolPolicy{
		TTL:             p.ttl,
		CaveatProducers: p.producers,
	}, nil
}

func TestGateway_CaveatProducers_BakedIntoToken(t *testing.T) {
	in, secret := defaultGatewayInputs()
	in.Context = map[string]any{
		"ci_status": "green",
		"label":     "auto-merge",
	}

	policyImpl := caveatProducerPolicy{
		ttl: time.Minute,
		producers: map[string]policy.CaveatProducer{
			"ci_status": func(in policy.EvaluationInput) (any, error) {
				return in.Context["ci_status"], nil
			},
			"label": func(in policy.EvaluationInput) (any, error) {
				return in.Context["label"], nil
			},
		},
	}

	g := &policy.GatewayEvaluator{
		Decider:    testharness.NewFakeAllow(),
		Secret:     secret,
		DefaultTTL: time.Minute,
		ToolPolicies: map[string]policy.ToolPolicy{
			in.Toolbox + ":" + in.Tool: policyImpl,
		},
	}
	result, err := g.EvaluateAndMint(context.Background(), in)
	require.NoError(t, err)

	sa := result.Authorization
	require.Equal(t, "green", sa.Caveats["ci_status"],
		"caveat producer's snapshot must travel into the token")
	require.Equal(t, "auto-merge", sa.Caveats["label"])
}

func TestGateway_CaveatProducerError_RecordsErrorInCaveat(t *testing.T) {
	in, secret := defaultGatewayInputs()
	failingPolicy := caveatProducerPolicy{
		ttl: time.Minute,
		producers: map[string]policy.CaveatProducer{
			"borked": func(_ policy.EvaluationInput) (any, error) {
				return nil, errors.New("simulated producer failure")
			},
		},
	}
	g := &policy.GatewayEvaluator{
		Decider:    testharness.NewFakeAllow(),
		Secret:     secret,
		DefaultTTL: time.Minute,
		ToolPolicies: map[string]policy.ToolPolicy{
			in.Toolbox + ":" + in.Tool: failingPolicy,
		},
	}
	result, err := g.EvaluateAndMint(context.Background(), in)
	require.NoError(t, err, "producer errors don't fail the mint; they record into caveat")

	sa := result.Authorization
	require.Contains(t, sa.Caveats["borked"].(string), "ERROR-COMPUTING-CAVEAT",
		"producer error surfaces in the caveat so the verifier rejects loudly")
}

// =====================================================================
// Built-in tool policies
// =====================================================================

func TestAllowAlwaysToolPolicy_PassesAnything(t *testing.T) {
	p := policy.AllowAlwaysToolPolicy{TTL: time.Minute, MaxUses: 5}
	out, err := p.Evaluate(context.Background(), policy.EvaluationInput{
		Tool: "anything",
	})
	require.NoError(t, err)
	require.Equal(t, time.Minute, out.TTL)
	require.Equal(t, 5, out.MaxUses)
}

func TestDenyAlwaysToolPolicy_RejectsWithReason(t *testing.T) {
	p := policy.DenyAlwaysToolPolicy{Reason: "blocked for incident"}
	_, err := p.Evaluate(context.Background(), policy.EvaluationInput{Tool: "x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "blocked for incident")
}

func TestDenyAlwaysToolPolicy_DefaultReason(t *testing.T) {
	p := policy.DenyAlwaysToolPolicy{}
	_, err := p.Evaluate(context.Background(), policy.EvaluationInput{Tool: "x"})
	require.Error(t, err)
	require.NotEmpty(t, err.Error())
}

func TestManifestCeilingPolicy_AllowsDeclared(t *testing.T) {
	manifest := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{
			{Action: "github.read_pr", Resource: "repo:*", Reason: "r"},
		},
	}
	p := policy.ManifestCeilingPolicy{Manifest: manifest, TTL: time.Minute}
	_, err := p.Evaluate(context.Background(), policy.EvaluationInput{
		Tool:     "github.read_pr",
		Resource: "repo:codefly/x",
	})
	require.NoError(t, err)
}

func TestManifestCeilingPolicy_DeniesUndeclared(t *testing.T) {
	manifest := policy.PermissionPolicy{
		Required: []policy.PermissionDeclaration{
			{Action: "github.read_pr", Resource: "repo:*", Reason: "r"},
		},
	}
	p := policy.ManifestCeilingPolicy{Manifest: manifest, TTL: time.Minute}
	_, err := p.Evaluate(context.Background(), policy.EvaluationInput{
		Tool:     "github.merge_pr", // not declared
		Resource: "repo:codefly/x",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "manifest does not declare")
}

// =====================================================================
// Global evaluator helpers
// =====================================================================

func TestGlobalGatewayEvaluator_RoundTrip(t *testing.T) {
	defer policy.SetGlobalGatewayEvaluator(nil) // cleanup

	require.Nil(t, policy.GetGlobalGatewayEvaluator(),
		"empty registry returns nil")

	g := &policy.GatewayEvaluator{
		Decider: testharness.NewFakeAllow(),
		Secret:  policy.NewSpawnSecret(),
	}
	policy.SetGlobalGatewayEvaluator(g)
	require.Same(t, g, policy.GetGlobalGatewayEvaluator())

	policy.SetGlobalGatewayEvaluator(nil)
	require.Nil(t, policy.GetGlobalGatewayEvaluator())
}
