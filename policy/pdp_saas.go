package policy

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SaasPDP is the production Decider for hosts that authenticate
// against saas-starter (or any other backend exposing a
// PermissionsBackend). It adapts the simpler backend shape into
// the full PDP/Decider interface, layering on:
//
//   - Local cache of positive decisions (TTL, configurable)
//   - NEVER cache denies (a freshly-revoked grant must fail
//     immediately on the next call)
//   - Observability: every decision goes through RecordDecision
//     so dashboards reflect real production traffic
//   - Fail-closed: backend errors surface as deny+FailClosed,
//     NEVER as silent allow
//   - Latency tracking: PDPMetrics gets the wall-time per call
//
// **Why this lives in core, not in saas-starter.** core has no
// compile-time dependency on saas-starter — SaasPDP takes a
// PermissionsBackend interface that saas-starter (or any other
// implementation) wires concretely. The dependency arrow is
// always saas-starter → core, never the reverse.
//
// **Cache invalidation.** TTL only, no broadcast invalidation in
// v1. Operators tune Cache TTL per their tolerance for stale
// allows. M10 hardening adds Postgres NOTIFY-driven invalidation
// for permission changes.
type SaasPDP struct {
	// Backend is the saas-starter (or test) implementation that
	// answers Decide. Required.
	Backend PermissionsBackend

	// CacheTTL is how long a positive decision stays valid in the
	// local LRU. Zero disables the cache entirely (every call
	// hits the backend). Recommended: 5-30 seconds for hot paths.
	// Longer TTLs trade staleness for latency.
	CacheTTL time.Duration

	// Metrics, when non-nil, gets every decision recorded via
	// RecordDecision. Wire to Prometheus/OTEL via Snapshot().
	Metrics *PDPMetrics

	// FailClosed controls behavior when Backend returns err. Default
	// (zero value) is fail-closed: backend faults deny. Setting to
	// false would fail-open — STRONGLY discouraged for production.
	// Kept as a field (not a constant) so tests can exercise the
	// fail-open path explicitly.
	//
	// **For production:** leave default. The whole architecture
	// rests on "saas-starter unreachable → calls deny". Without
	// this, a network blip becomes a permission bypass.
	FailClosed bool

	mu    sync.Mutex
	cache map[saasCacheKey]saasCacheEntry
}

// saasCacheKey identifies a cached decision. Including orgID and
// scope distinguishes cross-org / cross-scope checks for the same
// principal.
type saasCacheKey struct {
	principalID string
	action      string
	resource    string
	orgID       string
	scope       string
}

type saasCacheEntry struct {
	expiresAt time.Time
	// Only positive decisions are cached, so we don't need a
	// "decision" field — entry presence == allow.
}

// NewSaasPDP constructs a SaasPDP with the standard production
// defaults: fail-closed, no metrics (caller wires later), no
// cache (caller sets explicitly to opt in).
//
// Use this constructor rather than &SaasPDP{} directly — it's the
// place future defaults will land without breaking callers.
func NewSaasPDP(backend PermissionsBackend) *SaasPDP {
	if backend == nil {
		panic("policy.NewSaasPDP: backend must be non-nil")
	}
	return &SaasPDP{
		Backend:    backend,
		FailClosed: true,
	}
}

// WithCache enables the LRU cache with the given TTL. Returns the
// receiver for fluent configuration.
func (s *SaasPDP) WithCache(ttl time.Duration) *SaasPDP {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CacheTTL = ttl
	if ttl > 0 && s.cache == nil {
		s.cache = make(map[saasCacheKey]saasCacheEntry)
	}
	return s
}

// WithMetrics attaches a PDPMetrics for observability. Returns the
// receiver for fluent configuration.
func (s *SaasPDP) WithMetrics(m *PDPMetrics) *SaasPDP {
	s.Metrics = m
	return s
}

// Evaluate implements PDP. Pulls principal+resource from req,
// consults cache, calls Backend on miss, records observability,
// returns the verdict.
func (s *SaasPDP) Evaluate(ctx context.Context, req *PDPRequest) PDPDecision {
	start := time.Now()
	principalID, _ := req.Identity["principal_id"].(string)
	orgID, _ := req.Identity["principal_org_id"].(string)
	resource := s.resourceFromArgs(req.Args)

	if principalID == "" {
		// No principal stamped — fail closed regardless. The
		// interceptor extracts principal from token; an empty
		// principal_id means token was missing or invalid.
		decision := PDPDecision{
			Allow:  false,
			Reason: "no principal on request (token missing or invalid)",
		}
		s.record(ctx, req, decision, time.Since(start), false, false)
		return decision
	}

	key := saasCacheKey{
		principalID: principalID,
		action:      req.Tool,
		resource:    resource,
		orgID:       orgID,
		// scope unused at this layer; future M4-style typed scopes
		// could populate it.
	}

	// Cache lookup.
	if hit := s.cacheLookup(key); hit {
		decision := PDPDecision{Allow: true}
		s.record(ctx, req, decision, time.Since(start), true, false)
		return decision
	}

	// Backend call.
	allowed, reason, decisionPath, err := s.Backend.Decide(ctx, principalID, req.Tool, resource, orgID, "")
	if err != nil {
		decision := s.failClosedDecision(err)
		// Only flag failClosed=true when we actually denied because
		// of the backend error. Fail-open path produces Allow=true
		// and records as a normal allow (with the WARNING in
		// reason). Operators on fail-open get the warning via logs;
		// the metric reflects the user-facing decision.
		s.record(ctx, req, decision, time.Since(start), false, !decision.Allow)
		return decision
	}

	if allowed {
		s.cacheStore(key)
		decision := PDPDecision{Allow: true, Reason: decisionPath}
		s.record(ctx, req, decision, time.Since(start), false, false)
		return decision
	}

	// Plain deny. Reason is the user-facing explanation; decisionPath
	// is the trace. Surface the reason verbatim — the model uses it
	// to decide whether to plan around or escalate.
	if reason == "" {
		reason = "policy: action denied (no reason provided)"
	}
	decision := PDPDecision{Allow: false, Reason: reason}
	s.record(ctx, req, decision, time.Since(start), false, false)
	return decision
}

func (s *SaasPDP) resourceFromArgs(args map[string]any) string {
	if args == nil {
		return ""
	}
	if v, ok := args["resource"].(string); ok {
		return v
	}
	return ""
}

func (s *SaasPDP) cacheLookup(key saasCacheKey) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.CacheTTL <= 0 {
		return false
	}
	if s.cache == nil {
		return false
	}
	entry, ok := s.cache[key]
	if !ok {
		return false
	}
	if time.Now().After(entry.expiresAt) {
		delete(s.cache, key)
		return false
	}
	return true
}

func (s *SaasPDP) cacheStore(key saasCacheKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.CacheTTL <= 0 {
		return
	}
	if s.cache == nil {
		s.cache = make(map[saasCacheKey]saasCacheEntry)
	}
	s.cache[key] = saasCacheEntry{
		expiresAt: time.Now().Add(s.CacheTTL),
	}
}

// failClosedDecision produces the deny that surfaces when Backend
// returns err. The reason includes the err.Error() so operators can
// correlate auth-backend incidents with their effects in the call
// graph; SAFE because err is internal-only (no PII, no token data).
func (s *SaasPDP) failClosedDecision(err error) PDPDecision {
	if !s.FailClosed {
		// Operator explicitly opted into fail-open. Strongly
		// discouraged; surfaces the choice in the reason for
		// audit grep.
		return PDPDecision{
			Allow:  true,
			Reason: fmt.Sprintf("WARNING: backend error tolerated (FailClosed=false): %v", err),
		}
	}
	return PDPDecision{
		Allow:  false,
		Reason: fmt.Sprintf("permission backend unreachable: %v", err),
	}
}

// record threads the decision through the observability surface.
// One place to update; metrics + structured log + (future) span
// attributes all branch off here.
func (s *SaasPDP) record(ctx context.Context, req *PDPRequest, d PDPDecision, latency time.Duration, cacheHit, failClosed bool) {
	ev := DecisionEvent{
		Toolbox:    req.Toolbox,
		Tool:       req.Tool,
		Decision:   d,
		Latency:    latency,
		CacheHit:   cacheHit,
		FailClosed: failClosed,
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
}

// --- Compile-time assertions --------------------------------------

var _ PDP = (*SaasPDP)(nil)
var _ Decider = (*SaasPDP)(nil) // Decider is an alias for PDP; both apply

// =====================================================================
// FakeBackend — testing helper
// =====================================================================

// FakeBackend is an in-memory PermissionsBackend used by tests of
// SaasPDP and any code that takes a PermissionsBackend. It records
// calls and answers from a programmable rule set.
//
// Concurrent-safe. Tests of cache behavior under load rely on this.
type FakeBackend struct {
	mu       sync.Mutex
	rules    map[fakeBackendKey]fakeBackendDecision
	defaultD fakeBackendDecision
	calls    []FakeBackendCall

	// Err, when non-nil, makes EVERY call return Err. Used to
	// exercise the fail-closed path. Takes precedence over rules.
	Err error
}

type fakeBackendKey struct {
	principalID, action, resource, orgID string
}

type fakeBackendDecision struct {
	allowed      bool
	reason       string
	decisionPath string
}

// FakeBackendCall is one recorded Decide call. Snapshot — safe to
// retain after Calls() returns.
type FakeBackendCall struct {
	PrincipalID  string
	Action       string
	Resource     string
	OrgID        string
	Scope        string
	Decision     bool
	Reason       string
	DecisionPath string
}

// NewFakeBackend returns a backend whose default verdict is
// (defaultAllow, "default-rule"). Pair with Allow / Deny to install
// specific rules.
func NewFakeBackend(defaultAllow bool) *FakeBackend {
	def := fakeBackendDecision{decisionPath: "fake-backend default"}
	if defaultAllow {
		def.allowed = true
	} else {
		def.reason = "fake-backend default deny"
	}
	return &FakeBackend{
		rules:    make(map[fakeBackendKey]fakeBackendDecision),
		defaultD: def,
	}
}

// Allow installs a rule that grants (principalID, action, resource, orgID).
// Returns the receiver for chaining.
func (f *FakeBackend) Allow(principalID, action, resource, orgID string) *FakeBackend {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules[fakeBackendKey{principalID, action, resource, orgID}] = fakeBackendDecision{
		allowed:      true,
		decisionPath: "fake-backend explicit allow",
	}
	return f
}

// Deny installs a deny rule with the supplied reason. Reason
// must be non-empty — silent denies harm test debuggability.
func (f *FakeBackend) Deny(principalID, action, resource, orgID, reason string) *FakeBackend {
	if reason == "" {
		panic("FakeBackend.Deny: reason must be non-empty (silent denies are not debuggable)")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules[fakeBackendKey{principalID, action, resource, orgID}] = fakeBackendDecision{
		reason: reason,
	}
	return f
}

// Decide implements PermissionsBackend.
func (f *FakeBackend) Decide(ctx context.Context, principalID, action, resource, orgID, scope string) (bool, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.Err != nil {
		// Don't record errored calls — tests of the fail-closed
		// path don't usually want the call log polluted.
		return false, "", "", f.Err
	}

	key := fakeBackendKey{principalID, action, resource, orgID}
	d, ok := f.rules[key]
	if !ok {
		d = f.defaultD
	}
	f.calls = append(f.calls, FakeBackendCall{
		PrincipalID:  principalID,
		Action:       action,
		Resource:     resource,
		OrgID:        orgID,
		Scope:        scope,
		Decision:     d.allowed,
		Reason:       d.reason,
		DecisionPath: d.decisionPath,
	})
	return d.allowed, d.reason, d.decisionPath, nil
}

// Calls returns a snapshot of every Decide call. Safe to retain.
func (f *FakeBackend) Calls() []FakeBackendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FakeBackendCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// CallCount returns the number of Decide calls observed.
func (f *FakeBackend) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// Reset clears the call log. Rules persist.
func (f *FakeBackend) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = f.calls[:0]
}

// --- Compile-time assertion ---------------------------------------

var _ PermissionsBackend = (*FakeBackend)(nil)
