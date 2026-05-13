package policy_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/policy/testharness"
)

func TestShadowPDP_AlwaysAllows_EvenWhenInnerDenies(t *testing.T) {
	inner := testharness.NewFakeDeny()
	shadow := policy.NewShadowPDP(inner, nil)

	d := shadow.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.force_push",
	})
	require.True(t, d.Allow,
		"shadow mode must ALWAYS allow regardless of inner decision")
	require.Contains(t, d.Reason, "shadow-mode",
		"reason must self-identify so accidental surfacing is debuggable")
	require.Contains(t, d.Reason, "would-deny",
		"shadow reason must report the inner's would-have-been verdict")
}

func TestShadowPDP_RecordsInnerDecision_AsMetric(t *testing.T) {
	inner := testharness.NewFakeDeny()
	metrics := &policy.PDPMetrics{}
	shadow := policy.NewShadowPDP(inner, metrics)

	shadow.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.force_push",
	})

	snap := metrics.Snapshot()
	require.Equal(t, int64(1), snap.DecisionsTotal)
	require.Equal(t, int64(1), snap.DeniesTotal,
		"the INNER decision (deny) is what's recorded — operators graph this to see what would-have-been blocked")
	require.Equal(t, int64(0), snap.AllowsTotal,
		"shadow's outer Allow is NOT counted — that would defeat the purpose")
}

func TestShadowPDP_PassesInnerCallThrough(t *testing.T) {
	inner := testharness.NewFakeAllow()
	shadow := policy.NewShadowPDP(inner, nil)

	shadow.Evaluate(context.Background(), &policy.PDPRequest{
		Toolbox: "git", Tool: "git.status",
		Identity: map[string]any{"principal_id": "p-1"},
	})

	require.Equal(t, 1, inner.CallCount())
	require.Equal(t, "git.status", inner.LastCall().Tool)
	require.Equal(t, "p-1", inner.LastCall().Identity["principal_id"])
}

func TestShadowPDP_RequireApproval_ReportsInReason(t *testing.T) {
	inner := pdpStub(func(_ context.Context, _ *policy.PDPRequest) policy.PDPDecision {
		return policy.PDPDecision{
			Allow:           false,
			RequireApproval: true,
			Reason:          "needs human approval",
		}
	})
	shadow := policy.NewShadowPDP(inner, nil)
	d := shadow.Evaluate(context.Background(), &policy.PDPRequest{Toolbox: "x", Tool: "x.y"})
	require.True(t, d.Allow)
	require.Contains(t, d.Reason, "would-require-approval")
}

func TestNewShadowPDP_NilInner_Panics(t *testing.T) {
	require.Panics(t, func() {
		policy.NewShadowPDP(nil, nil)
	}, "nil inner is a misconfiguration; fail at startup, not silently allow forever")
}

func TestShadowPDP_NilInner_Direct_FallsBackToAllow(t *testing.T) {
	// Defensive path: directly-constructed ShadowPDP{} (zero value)
	// has Inner=nil. We allow rather than crash mid-request. The
	// constructor (NewShadowPDP) is the one that panics on nil.
	shadow := policy.ShadowPDP{}
	d := shadow.Evaluate(context.Background(), &policy.PDPRequest{Toolbox: "x"})
	require.True(t, d.Allow)
}

// pdpStub adapts a function to the PDP interface for ad-hoc test cases.
type pdpStub func(context.Context, *policy.PDPRequest) policy.PDPDecision

func (f pdpStub) Evaluate(ctx context.Context, req *policy.PDPRequest) policy.PDPDecision {
	return f(ctx, req)
}
