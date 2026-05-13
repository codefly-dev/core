package policy

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/codefly-dev/core/wool"
)

// PDPMetrics is the lightweight, in-process metric surface for the
// permission system. We intentionally don't depend on Prometheus or
// OpenTelemetry here:
//
//  1. core/ ships as a library; pulling in Prometheus drags a 10MB+
//     transitive dependency onto every plugin
//  2. operators who DO want Prometheus can wire their backend by
//     reading these counters periodically — the type stays simple
//  3. tests can assert against the counters directly without setting
//     up a metric backend
//
// The fields are atomic.Int64 so callers can read them concurrently
// with Decide loops. Use Snapshot() to grab a coherent set of values
// at a single instant.
type PDPMetrics struct {
	// DecisionsTotal is bumped on every Evaluate call, regardless
	// of outcome. Pair with AllowsTotal/DeniesTotal/RequireApprovalsTotal
	// to get the breakdown.
	DecisionsTotal atomic.Int64

	// AllowsTotal — successful authorizations.
	AllowsTotal atomic.Int64

	// DeniesTotal — refused authorizations. A spike here is the
	// signal that triggers operator alerts.
	DeniesTotal atomic.Int64

	// RequireApprovalsTotal — calls that returned RequireApproval
	// (M7+). Until the synchronous escalation flow lands, this stays
	// at zero in production.
	RequireApprovalsTotal atomic.Int64

	// CacheHitsTotal — Decide call short-circuited via the local
	// cache (see SaasPDP, M3+). Used to verify cache effectiveness
	// in load tests.
	CacheHitsTotal atomic.Int64

	// FailClosedTotal — PDP couldn't reach saas-starter (or other
	// backend failure) and defaulted to deny. Critical reliability
	// signal: spikes here mean the auth backend is unreachable.
	FailClosedTotal atomic.Int64

	// LatencyNanosTotal + LatencySamples let callers compute mean
	// decision latency without keeping a histogram. For p99-grade
	// observability operators should plug in a real histogram via
	// the wool span on each call (Tracer adds the span timing).
	LatencyNanosTotal atomic.Int64
	LatencySamples    atomic.Int64
}

// PDPMetricsSnapshot is an immutable copy of PDPMetrics at one instant.
// Returned by Snapshot() for stable reads.
type PDPMetricsSnapshot struct {
	DecisionsTotal        int64
	AllowsTotal           int64
	DeniesTotal           int64
	RequireApprovalsTotal int64
	CacheHitsTotal        int64
	FailClosedTotal       int64
	MeanLatency           time.Duration
}

// Snapshot returns the current values atomically. Concurrent calls
// to RecordDecision during a snapshot are safe — values may shift
// mid-read but each field is consistent on its own.
func (m *PDPMetrics) Snapshot() PDPMetricsSnapshot {
	totalNs := m.LatencyNanosTotal.Load()
	samples := m.LatencySamples.Load()
	var mean time.Duration
	if samples > 0 {
		mean = time.Duration(totalNs / samples)
	}
	return PDPMetricsSnapshot{
		DecisionsTotal:        m.DecisionsTotal.Load(),
		AllowsTotal:           m.AllowsTotal.Load(),
		DeniesTotal:           m.DeniesTotal.Load(),
		RequireApprovalsTotal: m.RequireApprovalsTotal.Load(),
		CacheHitsTotal:        m.CacheHitsTotal.Load(),
		FailClosedTotal:       m.FailClosedTotal.Load(),
		MeanLatency:           mean,
	}
}

// Reset zeros every counter. ONLY for tests — production metrics
// should never reset (counter resets are an anti-pattern in
// monitoring; use rate calculations instead).
func (m *PDPMetrics) Reset() {
	m.DecisionsTotal.Store(0)
	m.AllowsTotal.Store(0)
	m.DeniesTotal.Store(0)
	m.RequireApprovalsTotal.Store(0)
	m.CacheHitsTotal.Store(0)
	m.FailClosedTotal.Store(0)
	m.LatencyNanosTotal.Store(0)
	m.LatencySamples.Store(0)
}

// DecisionEvent describes one PDP decision for observability. Carries
// the structured fields downstream backends (logs, metrics, traces)
// need. Fields are flat / primitive so backends don't need codec
// tricks to serialize.
type DecisionEvent struct {
	// Toolbox + Tool — what was being authorized.
	Toolbox string
	Tool    string

	// PrincipalID — who was asking. Empty when no Principal was
	// stamped on the call (legacy paths).
	PrincipalID string

	// PrincipalKind — "human" | "service" | "agent" | "" if unset.
	PrincipalKind string

	// AgentID — publisher/name:version for kind=agent; empty otherwise.
	AgentID string

	// DelegationDepth — 0 if the call wasn't delegated; else the
	// length of the delegation chain. Useful for "show me deeply-
	// delegated calls" audit queries.
	DelegationDepth int

	// Decision — the verdict.
	Decision PDPDecision

	// Latency — wall time from PDP entry to verdict. Includes
	// network calls to the auth backend; useful for SLI alerting.
	Latency time.Duration

	// CacheHit — true if the decision came from local cache (no
	// backend round-trip).
	CacheHit bool

	// FailClosed — true if the backend was unreachable and the PDP
	// defaulted to deny. Distinct from a normal Deny.
	FailClosed bool
}

// RecordDecision is the central observability entry point. Call it
// once per PDP decision, AFTER the decision has been computed.
//
// Behavior:
//
//   - Increments the relevant counters on metrics (allow/deny/etc).
//   - Logs a structured wool event at the appropriate level
//     (Trace for allows, Info for denies, Warn for fail-closed).
//   - If a span is active on ctx, adds a span event with the same
//     fields so traces stay correlated.
//
// Pass nil for metrics to skip counter updates (legacy code paths
// during migration). Always passes a non-empty event spec.
func RecordDecision(ctx context.Context, metrics *PDPMetrics, ev DecisionEvent) {
	if metrics != nil {
		metrics.DecisionsTotal.Add(1)
		switch {
		case ev.FailClosed:
			metrics.FailClosedTotal.Add(1)
			metrics.DeniesTotal.Add(1) // fail-closed is also a deny
		case ev.Decision.Allow:
			metrics.AllowsTotal.Add(1)
		case ev.Decision.RequireApproval:
			metrics.RequireApprovalsTotal.Add(1)
		default:
			metrics.DeniesTotal.Add(1)
		}
		if ev.CacheHit {
			metrics.CacheHitsTotal.Add(1)
		}
		if ev.Latency > 0 {
			metrics.LatencyNanosTotal.Add(int64(ev.Latency))
			metrics.LatencySamples.Add(1)
		}
	}

	// Structured logging via wool. Keep field names stable — they
	// flow into log queries and dashboards.
	w := wool.Get(ctx).In("policy.RecordDecision")
	fields := []*wool.LogField{
		wool.Field("toolbox", ev.Toolbox),
		wool.Field("tool", ev.Tool),
		wool.Field("principal_id", ev.PrincipalID),
		wool.Field("principal_kind", ev.PrincipalKind),
		wool.Field("agent_id", ev.AgentID),
		wool.Field("delegation_depth", ev.DelegationDepth),
		wool.Field("decision_allow", ev.Decision.Allow),
		wool.Field("decision_reason", ev.Decision.Reason),
		wool.Field("decision_latency_ns", int64(ev.Latency)),
		wool.Field("cache_hit", ev.CacheHit),
		wool.Field("fail_closed", ev.FailClosed),
	}

	switch {
	case ev.FailClosed:
		// Fail-closed means the auth backend was unreachable. This is
		// an operational issue worth waking someone up about.
		w.Warn("PDP fail-closed (backend unreachable)", fields...)
	case ev.Decision.RequireApproval:
		// Approval-required is informational — common during
		// autonomous agent work; not a problem on its own.
		w.Info("PDP requires approval", fields...)
	case !ev.Decision.Allow:
		// Plain denials are interesting because the model will
		// surface them; logging at Info makes them queryable.
		w.Info("PDP deny", fields...)
	default:
		// Allows are noisy at scale — log at Trace so they're
		// available for forensics but don't dominate normal logs.
		w.Trace("PDP allow", fields...)
	}
}

// --- Decision shape extension --------------------------------------

// PDPDecision is extended elsewhere (pdp.go) but uses optional fields
// here for the M7+ approval flow. Until then, RequireApproval stays
// false and the field has no observable effect.

// RequireApproval signals that the PDP needs a human/grantor decision
// before the action can proceed. The agent SDK turns this into a
// RequestEscalation flow. Distinct from Deny because it's recoverable
// — the model can request approval and retry.
//
// (Located here in observability.go because the metrics treat it
// as a third counter category. The flag itself lives on the same
// PDPDecision type defined in pdp.go.)
