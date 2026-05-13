package policy_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
)

func TestPDPMetrics_AllowIncrementsAllowsAndDecisions(t *testing.T) {
	m := &policy.PDPMetrics{}
	policy.RecordDecision(context.Background(), m, policy.DecisionEvent{
		Toolbox:  "git",
		Tool:     "git.status",
		Decision: policy.PDPDecision{Allow: true},
		Latency:  5 * time.Millisecond,
	})

	snap := m.Snapshot()
	require.Equal(t, int64(1), snap.DecisionsTotal)
	require.Equal(t, int64(1), snap.AllowsTotal)
	require.Equal(t, int64(0), snap.DeniesTotal)
	require.Equal(t, int64(0), snap.RequireApprovalsTotal)
	require.Equal(t, int64(0), snap.FailClosedTotal)
	require.Equal(t, 5*time.Millisecond, snap.MeanLatency)
}

func TestPDPMetrics_DenyIncrementsDeniesAndDecisions(t *testing.T) {
	m := &policy.PDPMetrics{}
	policy.RecordDecision(context.Background(), m, policy.DecisionEvent{
		Toolbox:  "git",
		Tool:     "git.force_push",
		Decision: policy.PDPDecision{Allow: false, Reason: "no role grants this"},
	})

	snap := m.Snapshot()
	require.Equal(t, int64(1), snap.DecisionsTotal)
	require.Equal(t, int64(0), snap.AllowsTotal)
	require.Equal(t, int64(1), snap.DeniesTotal)
}

func TestPDPMetrics_RequireApproval_OwnCounter(t *testing.T) {
	m := &policy.PDPMetrics{}
	policy.RecordDecision(context.Background(), m, policy.DecisionEvent{
		Toolbox:  "github",
		Tool:     "github.merge_pr",
		Decision: policy.PDPDecision{Allow: false, RequireApproval: true, ApprovalRequestID: "ar-1"},
	})

	snap := m.Snapshot()
	require.Equal(t, int64(1), snap.DecisionsTotal)
	require.Equal(t, int64(0), snap.AllowsTotal,
		"RequireApproval is NOT an allow — pending approval is its own state")
	require.Equal(t, int64(0), snap.DeniesTotal,
		"RequireApproval is NOT a deny either — recoverable via escalation")
	require.Equal(t, int64(1), snap.RequireApprovalsTotal)
}

func TestPDPMetrics_FailClosed_BothDenyAndFailClosedIncrement(t *testing.T) {
	// Fail-closed is a deny PLUS a backend-availability signal; both
	// counters increment so on-call alerts can distinguish "policy
	// denied legitimate work" from "auth backend is down".
	m := &policy.PDPMetrics{}
	policy.RecordDecision(context.Background(), m, policy.DecisionEvent{
		Toolbox:    "git",
		Tool:       "git.status",
		Decision:   policy.PDPDecision{Allow: false, Reason: "saas-starter unreachable"},
		FailClosed: true,
	})

	snap := m.Snapshot()
	require.Equal(t, int64(1), snap.FailClosedTotal)
	require.Equal(t, int64(1), snap.DeniesTotal,
		"fail-closed must also count as deny so total deny rate stays accurate")
}

func TestPDPMetrics_CacheHit_IncrementsCounter(t *testing.T) {
	m := &policy.PDPMetrics{}
	policy.RecordDecision(context.Background(), m, policy.DecisionEvent{
		Toolbox:  "git",
		Tool:     "git.status",
		Decision: policy.PDPDecision{Allow: true},
		CacheHit: true,
		Latency:  100 * time.Microsecond,
	})

	snap := m.Snapshot()
	require.Equal(t, int64(1), snap.CacheHitsTotal)
	require.Equal(t, int64(1), snap.AllowsTotal,
		"cache hit doesn't change the allow/deny count")
}

func TestPDPMetrics_MeanLatency_AveragesAcrossCalls(t *testing.T) {
	m := &policy.PDPMetrics{}
	for _, latency := range []time.Duration{
		1 * time.Millisecond,
		3 * time.Millisecond,
		5 * time.Millisecond,
	} {
		policy.RecordDecision(context.Background(), m, policy.DecisionEvent{
			Toolbox: "x", Tool: "x.y",
			Decision: policy.PDPDecision{Allow: true},
			Latency:  latency,
		})
	}

	snap := m.Snapshot()
	require.Equal(t, 3*time.Millisecond, snap.MeanLatency,
		"mean of 1+3+5 should be 3 ms")
}

func TestPDPMetrics_ZeroLatency_NotIncluded(t *testing.T) {
	// Zero-duration calls (e.g. fully-cached returns) shouldn't
	// distort the mean — they're recorded but skip latency tracking.
	m := &policy.PDPMetrics{}
	policy.RecordDecision(context.Background(), m, policy.DecisionEvent{
		Toolbox: "x", Tool: "x.y",
		Decision: policy.PDPDecision{Allow: true},
		Latency:  0,
	})
	policy.RecordDecision(context.Background(), m, policy.DecisionEvent{
		Toolbox: "x", Tool: "x.y",
		Decision: policy.PDPDecision{Allow: true},
		Latency:  10 * time.Millisecond,
	})

	snap := m.Snapshot()
	require.Equal(t, 10*time.Millisecond, snap.MeanLatency,
		"zero-latency call must not be averaged in")
}

func TestPDPMetrics_NilTolerated(t *testing.T) {
	// Migration paths where metrics aren't wired yet pass nil. Must
	// not panic; logging should still happen.
	require.NotPanics(t, func() {
		policy.RecordDecision(context.Background(), nil, policy.DecisionEvent{
			Toolbox: "x", Tool: "x.y",
			Decision: policy.PDPDecision{Allow: true},
		})
	})
}

func TestPDPMetrics_Reset_ZerosAll(t *testing.T) {
	m := &policy.PDPMetrics{}
	policy.RecordDecision(context.Background(), m, policy.DecisionEvent{
		Toolbox: "x", Tool: "x.y",
		Decision: policy.PDPDecision{Allow: true},
		Latency:  time.Millisecond,
	})
	require.NotZero(t, m.Snapshot().DecisionsTotal)

	m.Reset()
	snap := m.Snapshot()
	require.Equal(t, int64(0), snap.DecisionsTotal)
	require.Equal(t, int64(0), snap.AllowsTotal)
	require.Equal(t, time.Duration(0), snap.MeanLatency)
}

func TestPDPMetrics_Concurrent_AccurateCounts(t *testing.T) {
	// Race detector validates atomicity; this also pins the count.
	m := &policy.PDPMetrics{}
	const N = 500
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := policy.PDPDecision{Allow: i%2 == 0}
			if !d.Allow {
				d.Reason = "alternating-deny"
			}
			policy.RecordDecision(context.Background(), m, policy.DecisionEvent{
				Toolbox: "x", Tool: "x.y",
				Decision: d, Latency: time.Microsecond,
			})
		}(i)
	}
	wg.Wait()

	snap := m.Snapshot()
	require.Equal(t, int64(N), snap.DecisionsTotal)
	require.Equal(t, int64(N/2), snap.AllowsTotal)
	require.Equal(t, int64(N/2), snap.DeniesTotal)
}

func TestPDPMetrics_Snapshot_StableUnderConcurrentWrites(t *testing.T) {
	// Snapshot fields are read independently; we don't promise
	// strict consistency across fields. But each individual field
	// must read a coherent value (atomic.Int64 guarantee).
	m := &policy.PDPMetrics{}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				policy.RecordDecision(context.Background(), m, policy.DecisionEvent{
					Toolbox: "x", Tool: "x.y",
					Decision: policy.PDPDecision{Allow: true},
				})
			}
		}
	}()

	// Run snapshots in parallel; if Load isn't atomic, the race
	// detector will flag.
	for i := 0; i < 1000; i++ {
		_ = m.Snapshot()
	}
	close(stop)
	wg.Wait()
}
