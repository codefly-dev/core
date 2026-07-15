package policy_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
)

func TestSaasPDP_NoPrincipal_FailsClosed(t *testing.T) {
	pdp := policy.NewSaasPDP(policy.NewFakeBackend(true))
	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Tool: "git.status", // no principal in Identity
	})
	require.False(t, d.Allow,
		"missing principal must fail closed — token was missing or invalid upstream")
	require.Contains(t, d.Reason, "principal")
}

func TestSaasPDP_BackendAllow_ReturnsAllow(t *testing.T) {
	be := policy.NewFakeBackend(false). // default deny
						Allow("p-1", "git.status", "repo:foo", "org-1")

	pdp := policy.NewSaasPDP(be)
	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Tool: "git.status",
		Args: map[string]any{"resource": "repo:foo"},
		Identity: map[string]any{
			"principal_id":     "p-1",
			"principal_org_id": "org-1",
		},
	})
	require.True(t, d.Allow)
}

func TestSaasPDP_BackendDeny_ReturnsDenyWithReason(t *testing.T) {
	be := policy.NewFakeBackend(true).
		Deny("p-1", "github.force_push", "repo:foo", "org-1", "force-push not allowed for this principal")

	pdp := policy.NewSaasPDP(be)
	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Tool: "github.force_push",
		Args: map[string]any{"resource": "repo:foo"},
		Identity: map[string]any{
			"principal_id":     "p-1",
			"principal_org_id": "org-1",
		},
	})
	require.False(t, d.Allow)
	require.Contains(t, d.Reason, "force-push not allowed",
		"backend's reason MUST surface verbatim — the model uses it to plan around")
}

func TestSaasPDP_BackendError_FailsClosed(t *testing.T) {
	be := policy.NewFakeBackend(true) // would allow if reachable
	be.Err = errors.New("connection refused")

	pdp := policy.NewSaasPDP(be)
	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Tool: "git.status",
		Identity: map[string]any{
			"principal_id":     "p-1",
			"principal_org_id": "org-1",
		},
	})
	require.False(t, d.Allow,
		"backend errors MUST fail closed — silent allow on infra failure is the architecture's worst-case bug")
	require.Contains(t, d.Reason, "connection refused",
		"the underlying error must surface for ops correlation")
	require.Contains(t, d.Reason, "unreachable")
}

func TestSaasPDP_FailOpen_ExplicitOptIn(t *testing.T) {
	be := policy.NewFakeBackend(true)
	be.Err = errors.New("transient fault")
	metrics := &policy.PDPMetrics{}
	pdp := policy.NewSaasPDP(be).WithMetrics(metrics)
	pdp.FailClosed = false // operator explicitly opted into fail-open

	d := pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Tool: "git.status",
		Identity: map[string]any{
			"principal_id":     "p-1",
			"principal_org_id": "org-1",
		},
	})
	require.True(t, d.Allow)
	require.Contains(t, d.Reason, "WARNING",
		"fail-open allows MUST surface a WARNING in the reason for audit grep")
	require.Contains(t, d.Reason, "FailClosed=false")

	// Accounting: fail-open produces an Allow, NOT a fail-closed
	// deny. Metrics reflect the user-facing outcome.
	snap := metrics.Snapshot()
	require.Equal(t, int64(1), snap.AllowsTotal,
		"fail-open allow counts as an allow")
	require.Equal(t, int64(0), snap.FailClosedTotal,
		"fail-open path is NOT fail-closed — the operator chose to ignore the backend error")
}

func TestSaasPDP_Cache_HitDoesNotCallBackend(t *testing.T) {
	be := policy.NewFakeBackend(false).
		Allow("p-1", "git.status", "repo:foo", "org-1")

	pdp := policy.NewSaasPDP(be).WithCache(time.Hour)

	req := &policy.PDPRequest{
		Tool: "git.status",
		Args: map[string]any{"resource": "repo:foo"},
		Identity: map[string]any{
			"principal_id":     "p-1",
			"principal_org_id": "org-1",
		},
	}

	d1 := pdp.Evaluate(context.Background(), req)
	require.True(t, d1.Allow)
	require.Equal(t, 1, be.CallCount())

	d2 := pdp.Evaluate(context.Background(), req)
	require.True(t, d2.Allow)
	require.Equal(t, 1, be.CallCount(),
		"cache hit must not call the backend a second time")
}

func TestSaasPDP_Cache_DenyNotCached(t *testing.T) {
	be := policy.NewFakeBackend(true).
		Deny("p-1", "github.force_push", "", "org-1", "denied for now")

	pdp := policy.NewSaasPDP(be).WithCache(time.Hour)
	req := &policy.PDPRequest{
		Tool: "github.force_push",
		Identity: map[string]any{
			"principal_id":     "p-1",
			"principal_org_id": "org-1",
		},
	}

	d1 := pdp.Evaluate(context.Background(), req)
	require.False(t, d1.Allow)
	require.Equal(t, 1, be.CallCount())

	d2 := pdp.Evaluate(context.Background(), req)
	require.False(t, d2.Allow)
	require.Equal(t, 2, be.CallCount(),
		"deny must NOT be cached — a freshly-revoked grant must fail immediately on retry")
}

func TestSaasPDP_Cache_ExpiresAfterTTL(t *testing.T) {
	be := policy.NewFakeBackend(false).
		Allow("p-1", "git.status", "", "org-1")

	pdp := policy.NewSaasPDP(be).WithCache(50 * time.Millisecond)
	req := &policy.PDPRequest{
		Tool: "git.status",
		Identity: map[string]any{
			"principal_id":     "p-1",
			"principal_org_id": "org-1",
		},
	}

	pdp.Evaluate(context.Background(), req)
	require.Equal(t, 1, be.CallCount())

	time.Sleep(70 * time.Millisecond)

	pdp.Evaluate(context.Background(), req)
	require.Equal(t, 2, be.CallCount(),
		"after TTL, cache must miss and the backend gets called again")
}

func TestSaasPDP_Cache_Disabled_AlwaysCallsBackend(t *testing.T) {
	be := policy.NewFakeBackend(false).
		Allow("p-1", "git.status", "", "org-1")

	pdp := policy.NewSaasPDP(be) // no WithCache → cache disabled
	req := &policy.PDPRequest{
		Tool: "git.status",
		Identity: map[string]any{
			"principal_id":     "p-1",
			"principal_org_id": "org-1",
		},
	}

	for i := 0; i < 5; i++ {
		pdp.Evaluate(context.Background(), req)
	}
	require.Equal(t, 5, be.CallCount(),
		"cache=0 means every call hits the backend (intentional for paranoid configs)")
}

func TestSaasPDP_Cache_DistinctKeys(t *testing.T) {
	// Same principal, different (action, resource, orgID) — each
	// must be cached independently.
	be := policy.NewFakeBackend(false).
		Allow("p-1", "git.status", "repo:foo", "org-1").
		Allow("p-1", "git.status", "repo:bar", "org-1").
		Allow("p-1", "git.commit", "repo:foo", "org-1")

	pdp := policy.NewSaasPDP(be).WithCache(time.Hour)
	mk := func(action, resource string) *policy.PDPRequest {
		return &policy.PDPRequest{
			Tool: action,
			Args: map[string]any{"resource": resource},
			Identity: map[string]any{
				"principal_id":     "p-1",
				"principal_org_id": "org-1",
			},
		}
	}

	pdp.Evaluate(context.Background(), mk("git.status", "repo:foo"))
	pdp.Evaluate(context.Background(), mk("git.status", "repo:bar"))
	pdp.Evaluate(context.Background(), mk("git.commit", "repo:foo"))
	require.Equal(t, 3, be.CallCount())

	// Re-issue all three; each should hit cache.
	pdp.Evaluate(context.Background(), mk("git.status", "repo:foo"))
	pdp.Evaluate(context.Background(), mk("git.status", "repo:bar"))
	pdp.Evaluate(context.Background(), mk("git.commit", "repo:foo"))
	require.Equal(t, 3, be.CallCount(), "all three keys cached distinctly")
}

func TestSaasPDP_Cache_EvictsLeastRecentlyUsed(t *testing.T) {
	be := policy.NewFakeBackend(true)
	pdp := policy.NewSaasPDP(be).WithCacheLimit(time.Hour, 2)
	mk := func(action string) *policy.PDPRequest {
		return &policy.PDPRequest{
			Tool: action,
			Identity: map[string]any{
				"principal_id":     "p-1",
				"principal_org_id": "org-1",
			},
		}
	}

	pdp.Evaluate(context.Background(), mk("tool.a"))
	pdp.Evaluate(context.Background(), mk("tool.b"))
	pdp.Evaluate(context.Background(), mk("tool.a")) // a is most-recently used
	pdp.Evaluate(context.Background(), mk("tool.c")) // evicts b
	require.Equal(t, 3, be.CallCount())

	pdp.Evaluate(context.Background(), mk("tool.a"))
	require.Equal(t, 3, be.CallCount(), "recent entry should remain cached")
	pdp.Evaluate(context.Background(), mk("tool.b"))
	require.Equal(t, 4, be.CallCount(), "least-recently used entry should be evicted")
}

func TestSaasPDP_CacheLimitRejectsInvalidBound(t *testing.T) {
	require.Panics(t, func() {
		policy.NewSaasPDP(policy.NewFakeBackend(true)).WithCacheLimit(time.Minute, 0)
	})
}

func TestSaasPDP_Metrics_RecordsAllowsAndDenies(t *testing.T) {
	be := policy.NewFakeBackend(false).
		Allow("p-1", "git.status", "", "org-1")
	metrics := &policy.PDPMetrics{}
	pdp := policy.NewSaasPDP(be).WithMetrics(metrics)

	id := map[string]any{"principal_id": "p-1", "principal_org_id": "org-1"}

	pdp.Evaluate(context.Background(), &policy.PDPRequest{Tool: "git.status", Identity: id})
	pdp.Evaluate(context.Background(), &policy.PDPRequest{Tool: "git.commit", Identity: id})

	snap := metrics.Snapshot()
	require.Equal(t, int64(2), snap.DecisionsTotal)
	require.Equal(t, int64(1), snap.AllowsTotal)
	require.Equal(t, int64(1), snap.DeniesTotal)
}

func TestSaasPDP_Metrics_CacheHitCounted(t *testing.T) {
	be := policy.NewFakeBackend(false).
		Allow("p-1", "git.status", "", "org-1")
	metrics := &policy.PDPMetrics{}
	pdp := policy.NewSaasPDP(be).WithCache(time.Hour).WithMetrics(metrics)

	req := &policy.PDPRequest{
		Tool:     "git.status",
		Identity: map[string]any{"principal_id": "p-1", "principal_org_id": "org-1"},
	}
	pdp.Evaluate(context.Background(), req) // miss
	pdp.Evaluate(context.Background(), req) // hit

	snap := metrics.Snapshot()
	require.Equal(t, int64(2), snap.DecisionsTotal)
	require.Equal(t, int64(1), snap.CacheHitsTotal)
}

func TestSaasPDP_Metrics_FailClosedCounted(t *testing.T) {
	be := policy.NewFakeBackend(true)
	be.Err = errors.New("net down")
	metrics := &policy.PDPMetrics{}
	pdp := policy.NewSaasPDP(be).WithMetrics(metrics)

	pdp.Evaluate(context.Background(), &policy.PDPRequest{
		Tool:     "git.status",
		Identity: map[string]any{"principal_id": "p-1", "principal_org_id": "org-1"},
	})

	snap := metrics.Snapshot()
	require.Equal(t, int64(1), snap.FailClosedTotal)
	require.Equal(t, int64(1), snap.DeniesTotal,
		"fail-closed counts as both fail-closed and deny — operator alerts split on both")
}

func TestSaasPDP_NewSaasPDP_NilBackend_Panics(t *testing.T) {
	require.Panics(t, func() {
		policy.NewSaasPDP(nil)
	})
}

func TestSaasPDP_Concurrent_CacheSafe(t *testing.T) {
	// Race detector validates the locking; this ensures correctness
	// under load. Many goroutines, same key — only one backend call
	// should happen if cache works correctly. (More can happen if
	// multiple miss before any wins; that's acceptable.)
	be := policy.NewFakeBackend(false).
		Allow("p-1", "git.status", "", "org-1")
	pdp := policy.NewSaasPDP(be).WithCache(time.Hour)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pdp.Evaluate(context.Background(), &policy.PDPRequest{
				Tool:     "git.status",
				Identity: map[string]any{"principal_id": "p-1", "principal_org_id": "org-1"},
			})
		}()
	}
	wg.Wait()
	require.LessOrEqual(t, be.CallCount(), 100, "no infinite loop / leak")
	require.GreaterOrEqual(t, be.CallCount(), 1, "at least one call must reach the backend")
}

// =====================================================================
// FakeBackend tests
// =====================================================================

func TestFakeBackend_Default_Deny(t *testing.T) {
	be := policy.NewFakeBackend(false)
	allowed, reason, _, err := be.Decide(context.Background(), "p", "a", "r", "o", "")
	require.NoError(t, err)
	require.False(t, allowed)
	require.NotEmpty(t, reason)
}

func TestFakeBackend_Default_Allow(t *testing.T) {
	be := policy.NewFakeBackend(true)
	allowed, _, _, err := be.Decide(context.Background(), "p", "a", "r", "o", "")
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestFakeBackend_Calls_RecordedInOrder(t *testing.T) {
	be := policy.NewFakeBackend(true)
	be.Decide(context.Background(), "p1", "a1", "r1", "o", "s1") //nolint:errcheck
	be.Decide(context.Background(), "p2", "a2", "r2", "o", "s2") //nolint:errcheck

	calls := be.Calls()
	require.Len(t, calls, 2)
	require.Equal(t, "p1", calls[0].PrincipalID)
	require.Equal(t, "s1", calls[0].Scope)
	require.Equal(t, "p2", calls[1].PrincipalID)
}

func TestFakeBackend_Err_TakesPrecedence(t *testing.T) {
	be := policy.NewFakeBackend(true).
		Allow("p", "a", "r", "o")
	be.Err = errors.New("simulated outage")

	_, _, _, err := be.Decide(context.Background(), "p", "a", "r", "o", "")
	require.Error(t, err)
	require.Equal(t, "simulated outage", err.Error())
	require.Equal(t, 0, be.CallCount(), "errored calls are NOT recorded — keeps test logs clean")
}

func TestFakeBackend_DenyEmptyReason_Panics(t *testing.T) {
	require.Panics(t, func() {
		policy.NewFakeBackend(true).Deny("p", "a", "r", "o", "")
	})
}

func TestFakeBackend_Reset_KeepsRules(t *testing.T) {
	be := policy.NewFakeBackend(false).
		Allow("p", "a", "r", "o")
	be.Decide(context.Background(), "p", "a", "r", "o", "") //nolint:errcheck
	require.Equal(t, 1, be.CallCount())
	be.Reset()
	require.Equal(t, 0, be.CallCount())
	allowed, _, _, _ := be.Decide(context.Background(), "p", "a", "r", "o", "")
	require.True(t, allowed, "rules persist after Reset")
}
