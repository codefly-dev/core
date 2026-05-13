package policy

import (
	"context"
	"time"
)

// ShadowPDP wraps another PDP and logs its decisions, but ALWAYS
// returns Allow. Used during M5 production rollout: operators flip
// `CODEFLY_PDP_MODE=shadow` before flipping to enforce, watch the
// audit logs for false-deny patterns, and fix policy drift before
// any developer actually gets locked out of their own tools.
//
// **Design contract:**
//
//   - Inner.Evaluate is called for every request — produces the
//     decision the operator WOULD see in enforce mode.
//   - Decision is logged via RecordDecision (Counter +
//     structured wool entry) so dashboards show "what would have
//     been denied" without affecting plugin behavior.
//   - Returned decision is always Allow with a reason that makes
//     the shadow nature obvious in any error surface that might
//     accidentally surface it. (It shouldn't — shadow always
//     allows — but defense at the boundary is cheap.)
//
// **What this is NOT:**
//
//   - Not a fail-closed wrapper. ShadowPDP can't fail closed by
//     design. If the inner PDP errors, ShadowPDP still allows.
//     The error is logged; no enforcement.
//   - Not for production enforce mode. Production must use the
//     bare PDP (not Shadow). Operators graduate from Shadow →
//     bare PDP after burn-in.
//
// Usage:
//
//	innerPDP := NewSaasPDP(...)
//	pdp := ShadowPDP{Inner: innerPDP, Metrics: &metrics}
//	// Plug pdp into PluginRegistration.PDP — every tool call now
//	// surfaces "what would have happened" in observability.
type ShadowPDP struct {
	// Inner is the real PDP whose decisions are recorded but
	// ignored. Required.
	Inner PDP

	// Metrics, if non-nil, gets the standard counters bumped per
	// the inner PDP's decision. Lets operators graph "shadow-mode
	// would-deny rate" alongside production allow rate.
	Metrics *PDPMetrics
}

// Evaluate runs Inner, records the decision, and returns Allow.
func (s ShadowPDP) Evaluate(ctx context.Context, req *PDPRequest) PDPDecision {
	if s.Inner == nil {
		// Defensive: a misconfigured ShadowPDP with no inner is
		// equivalent to AllowAllPDP. Don't crash; production must
		// catch this with a startup-time check (the ShadowPDP
		// constructor below requires Inner).
		return PDPDecision{Allow: true}
	}

	start := time.Now()
	decision := s.Inner.Evaluate(ctx, req)
	latency := time.Since(start)

	// Record the SHADOW decision (what would have happened) in
	// observability. The actual returned decision below is always
	// Allow — but the metric counters reflect the inner PDP's
	// verdict so operators can graph shadow-deny rates.
	ev := DecisionEvent{
		Toolbox:  req.Toolbox,
		Tool:     req.Tool,
		Decision: decision,
		Latency:  latency,
	}
	if id, ok := req.Identity["principal_id"].(string); ok {
		ev.PrincipalID = id
	}
	if kind, ok := req.Identity["principal_kind"].(string); ok {
		ev.PrincipalKind = kind
	}
	if agentID, ok := req.Identity["agent_id"].(string); ok {
		ev.AgentID = agentID
	}
	if chain, ok := req.Identity["delegation_chain"].([]map[string]any); ok {
		ev.DelegationDepth = len(chain)
	}
	RecordDecision(ctx, s.Metrics, ev)

	// ALWAYS allow, regardless of inner verdict.
	return PDPDecision{
		Allow:  true,
		Reason: "shadow-mode: inner=" + decisionReasonForShadow(decision),
	}
}

// decisionReasonForShadow turns the inner decision into a
// debuggable string for log/audit surface. Operator dashboards
// pivot on this when triaging "why would this have been denied?"
func decisionReasonForShadow(d PDPDecision) string {
	switch {
	case d.RequireApproval:
		return "would-require-approval (" + d.Reason + ")"
	case d.Allow:
		return "allow"
	default:
		return "would-deny (" + d.Reason + ")"
	}
}

// NewShadowPDP constructs a ShadowPDP with required Inner. Panics
// if inner is nil — startup-time check, never silently no-op.
func NewShadowPDP(inner PDP, metrics *PDPMetrics) ShadowPDP {
	if inner == nil {
		panic("policy.NewShadowPDP: inner PDP must be non-nil (use AllowAllPDP if you want unconditional allow without shadow recording)")
	}
	return ShadowPDP{Inner: inner, Metrics: metrics}
}

// --- Compile-time interface assertion ------------------------------

var _ PDP = ShadowPDP{}
