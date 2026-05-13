package policy

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/codefly-dev/core/wool"
)

// =====================================================================
// M10 hardening — defense-in-depth additions
// =====================================================================
//
// Three independent hardening features that operators can enable
// once the basic permission flow is in production:
//
//   - Token Revocation List (TRL): even valid-signature tokens
//     get rejected if their id is on the revocation list.
//     Compromised tokens get yanked without waiting for expiry.
//
//   - Break-glass: a deliberate, audit-loud bypass for incident
//     response. Operator sets CODEFLY_BREAK_GLASS_JUSTIFICATION;
//     all calls bypass the PDP for that ONE process. Every call
//     emits a WARN log line so the audit trail is unmistakable.
//
//   - Recursion depth caps: bounds how deep a delegation chain
//     can go. Default 3 (user → orchestrator → leaf). Tokens
//     whose chain exceeds the cap are rejected — defends against
//     pathological "agent escalates from agent escalates from
//     agent" scenarios that could otherwise mask the originator.

// =====================================================================
// Token Revocation List (TRL)
// =====================================================================

// TokenRevocationList holds a set of token IDs that must be
// rejected even with valid signatures. Used to invalidate
// compromised or otherwise-suspect tokens before they expire.
//
// **Where this fits.** Verify (v1 + v2) accepts tokens based on
// signature + claims. The TRL is consulted INSIDE the Guard
// alongside the replay tracker — both check token id; replay
// rejects "seen before for this token", TRL rejects "this token
// is revoked, period".
//
// **Distribution.** Operators populate TRL in two ways:
//
//   1. Programmatic: call Add(tokenID) when receiving a
//      revocation event (e.g. from saas-starter via
//      Postgres NOTIFY or webhook).
//
//   2. File-backed: TrackFile(path) watches a JSON file
//      containing the revoked-id list. Reload on file change.
//      Useful for kubectl-style ConfigMap distribution.
//
// **Why not "always check saas-starter on every call".** It'd
// be trivially correct, but the M3-era PDP cache exists exactly
// to avoid that round-trip on every request. TRL is the
// invalidation channel for the cache: revocations push, the
// cache reads from local TRL.
type TokenRevocationList struct {
	mu      sync.RWMutex
	revoked map[string]struct{}
}

// NewTokenRevocationList returns an empty TRL.
func NewTokenRevocationList() *TokenRevocationList {
	return &TokenRevocationList{revoked: map[string]struct{}{}}
}

// Add marks tokenID as revoked. Idempotent: re-adding the same
// id is a no-op.
func (t *TokenRevocationList) Add(tokenID string) {
	if tokenID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.revoked[tokenID] = struct{}{}
}

// AddMany is the bulk variant. Used by file-backed TRL reload.
func (t *TokenRevocationList) AddMany(tokenIDs []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range tokenIDs {
		if id == "" {
			continue
		}
		t.revoked[id] = struct{}{}
	}
}

// Remove unrevokes a token id. Useful for tests; rare in
// production (revocations are typically one-way).
func (t *TokenRevocationList) Remove(tokenID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.revoked, tokenID)
}

// IsRevoked reports whether tokenID is on the list. The Guard
// calls this after a successful signature+claims verify and
// rejects the token if true.
func (t *TokenRevocationList) IsRevoked(tokenID string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.revoked[tokenID]
	return ok
}

// Size returns the count of revoked tokens. Used in tests +
// observability.
func (t *TokenRevocationList) Size() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.revoked)
}

// Replace atomically swaps the entire revocation set. Used by
// file-backed reload to avoid intermediate states where the TRL
// is partially updated.
func (t *TokenRevocationList) Replace(tokenIDs []string) {
	updated := make(map[string]struct{}, len(tokenIDs))
	for _, id := range tokenIDs {
		if id != "" {
			updated[id] = struct{}{}
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.revoked = updated
}

// =====================================================================
// Break-glass override
// =====================================================================
//
// **What this is.** A deliberate, audit-loud bypass for incident
// response. Operator sets CODEFLY_BREAK_GLASS_JUSTIFICATION to a
// non-empty string; the running plugin process treats every PDP
// call as allow but logs WARN with the justification on every
// CallTool. Used when normal authorization is itself the
// problem (the auth backend is down, or a bug is denying
// legitimate work).
//
// **Why this exists.** Locked-down systems become unusable in
// emergencies. Without an explicit break-glass, operators
// improvise — disabling features, faking principals, etc. —
// and those improvisations don't show up in audit logs. A
// first-class break-glass is greppable, auditable, and bounded
// to one process.
//
// **Audit semantics.** Every break-glass-bypassed call logs at
// WARN level with:
//
//   - the justification string
//   - the principal_id (if any) on ctx
//   - the action being bypassed
//   - the resource (if any)
//
// SOC2 / SOX-style audits SHOULD flag any window where any
// process had break-glass enabled. The startup banner (logged
// on first call) makes the window's start unmistakable.

// EnvBreakGlass is the env var that controls break-glass mode.
// Non-empty value = break-glass active; the value is the
// mandatory justification.
const EnvBreakGlass = "CODEFLY_BREAK_GLASS_JUSTIFICATION"

// breakGlassJustification holds the cached env value at process
// startup. Atomic so concurrent reads don't race with the
// startup banner write.
var breakGlassJustification atomic.Pointer[string]

// breakGlassOnce ensures the startup-banner WARN fires exactly
// once per process when break-glass is first observed.
var breakGlassOnce sync.Once

// IsBreakGlassActive reports whether the env var is set with a
// non-empty justification. Cached after first read.
//
// **Operator note.** Setting this env var is auditable on its
// own — it leaves a trail in process startup logs, ConfigMap
// changes, etc. The point is not secrecy (you announce it);
// it's TRACEABILITY. "Was break-glass active during 2025-04-12
// 14:00 UTC?" is a one-grep question.
func IsBreakGlassActive() bool {
	return breakGlassReason() != ""
}

// breakGlassReason returns the configured justification, or
// empty if break-glass is inactive. Reads env once, caches.
func breakGlassReason() string {
	if cached := breakGlassJustification.Load(); cached != nil {
		return *cached
	}
	v := os.Getenv(EnvBreakGlass)
	breakGlassJustification.Store(&v)
	return v
}

// LogBreakGlassUsage emits the WARN audit event for a
// break-glass-bypassed call. Caller is the Guard at CallTool
// time. Does NOT panic if break-glass is inactive (defensive);
// returns silently.
func LogBreakGlassUsage(ctx context.Context, action, resource string) {
	reason := breakGlassReason()
	if reason == "" {
		return
	}
	// Fire the once-per-process startup banner the first time
	// any break-glass call is observed.
	breakGlassOnce.Do(func() {
		wool.Get(ctx).In("policy.BreakGlass").
			Warn("BREAK-GLASS ACTIVE: every PDP decision will be bypassed for this process. ALL calls audit at WARN.",
				wool.Field("justification", reason),
				wool.Field("env_var", EnvBreakGlass))
	})

	pid := ""
	pkind := ""
	if p := PrincipalFrom(ctx); p != nil {
		pid = p.ID
		pkind = p.Kind
	}
	wool.Get(ctx).In("policy.BreakGlass").
		Warn("BREAK-GLASS bypass on call",
			wool.Field("justification", reason),
			wool.Field("action", action),
			wool.Field("resource", resource),
			wool.Field("principal_id", pid),
			wool.Field("principal_kind", pkind))
}

// =====================================================================
// Recursion depth caps on delegation chains
// =====================================================================
//
// **What this is.** A hard cap on how many hops a delegation
// chain can have. Default 3 (typical: human → orchestrator →
// leaf agent). Tokens whose Principal.DelegationChain length
// exceeds the cap are rejected at verify time.
//
// **Why this matters.** Delegation chains let authority flow
// from one principal to another (M7 escalation, M8 patterns).
// Without a cap, an attacker who compromises ANY agent in the
// chain could mint tokens that delegate further, hiding the
// originator behind layers. Capping the depth bounds the
// complexity of the audit trail and prevents pathological
// "delegation through 17 agents" patterns.
//
// **Default 3.** Realistic chains:
//
//   - User invokes Mind: chain=[user] (depth 1)
//   - Mind invokes auto-merge-bot: chain=[user, mind] (depth 2)
//   - auto-merge-bot escalates to a different role: chain=[user, mind, escalation-grantor] (depth 3)
//
// Beyond 3 is suspicious. Operators with legitimate need
// (multi-tenant orchestration with org-bridge agents) override
// via CODEFLY_MAX_DELEGATION_DEPTH.

// EnvMaxDelegationDepth overrides the default depth cap.
const EnvMaxDelegationDepth = "CODEFLY_MAX_DELEGATION_DEPTH"

// DefaultMaxDelegationDepth is the per-process cap when the env
// var is unset.
const DefaultMaxDelegationDepth = 3

// MaxDelegationDepth returns the configured per-process cap.
// Reads env once, caches. Invalid values (non-int, negative)
// fall back to the default with a WARN log on first call.
func MaxDelegationDepth() int {
	cached := maxDelegationDepthCache.Load()
	if cached > 0 {
		return int(cached)
	}
	v := os.Getenv(EnvMaxDelegationDepth)
	if v == "" {
		maxDelegationDepthCache.Store(DefaultMaxDelegationDepth)
		return DefaultMaxDelegationDepth
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		// Misconfiguration: log loud, fall back to default.
		// Don't fail-closed (rejecting all tokens) on a typo —
		// that locks operators out worse than allowing the
		// default cap.
		maxDelegationDepthOnce.Do(func() {
			fmt.Fprintf(os.Stderr,
				"[policy] %s=%q is not a positive integer; using default %d\n",
				EnvMaxDelegationDepth, v, DefaultMaxDelegationDepth)
		})
		maxDelegationDepthCache.Store(DefaultMaxDelegationDepth)
		return DefaultMaxDelegationDepth
	}
	maxDelegationDepthCache.Store(int32(n))
	return n
}

var (
	maxDelegationDepthCache atomic.Int32
	maxDelegationDepthOnce  sync.Once
)

// CheckDelegationDepth returns an error if the principal's
// delegation chain exceeds the configured cap. Used by the
// Guard alongside Verify; called after signature verification
// passes but before dispatching the call.
//
// nil principal or empty chain always pass — no chain to cap.
func CheckDelegationDepth(p *Principal) error {
	if p == nil || len(p.DelegationChain) == 0 {
		return nil
	}
	cap := MaxDelegationDepth()
	if len(p.DelegationChain) > cap {
		return fmt.Errorf("delegation chain depth %d exceeds cap %d (CODEFLY_MAX_DELEGATION_DEPTH)",
			len(p.DelegationChain), cap)
	}
	return nil
}

// =====================================================================
// Test-only helpers — reset cached env reads
// =====================================================================

// ResetHardeningCachesForTest clears the cached env reads so
// tests can drive different env values per case via t.Setenv.
// **Exported for tests only** — production code MUST NOT call
// this. The exposed-for-test pattern is the cleanest way to
// avoid build-tag gymnastics; the function name's _ForTest
// suffix is the convention.
func ResetHardeningCachesForTest() {
	breakGlassJustification.Store(nil)
	maxDelegationDepthCache.Store(0)
}
