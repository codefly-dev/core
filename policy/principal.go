package policy

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Principal is the unified identity type for everything codefly
// authorizes — humans, services, and agents. It exists at the core
// layer so the runners, agent loader, and PDP can all speak the same
// vocabulary without one of them owning the others.
//
// **Why one type for humans + services + agents.** The auth layer
// shouldn't care whether a row in saas-starter's principals table
// represents Antoine, the auto-merge bot, or Mind. They all hold
// permissions via the same role-assignment table. Kind is metadata
// for the UI ("show me my agents") and audit ("filter to humans"),
// not a fundamental of the auth model. Treating them uniformly is
// what lets a service principal request escalation from a human
// principal using the same delegation primitive that a user uses
// to invoke an agent.
//
// **What this type is NOT.** It's not the credential. The credential
// (JWT/Biscuit/...) lives in Token; Principal is the resolved
// identity claims after the credential has been verified. Code that
// receives a Principal can trust the fields — verification has
// already happened upstream.
type Principal struct {
	// ID is the saas-starter principals.id UUID. Stable across
	// credential rotations; this is what audit logs reference and
	// what role assignments target.
	ID string

	// Kind is the principal's flavor: "human", "service", or "agent".
	// Used for filtering and UI affordances. NEVER branch on Kind for
	// authorization decisions — that's the auth layer's job, and
	// branching here re-introduces the special-casing the unified
	// model exists to avoid.
	Kind string

	// OrgID is the organization the principal belongs to. Cross-org
	// access requires an explicit cross-org grant, never inferred
	// from the principal alone.
	OrgID string

	// AgentID is the publisher/name:version identifier when Kind is
	// "agent"; empty otherwise. Lets the host correlate a Principal
	// back to a specific agent manifest at install time.
	AgentID string

	// DisplayName is human-readable. NEVER use as an identity key
	// (display names change; IDs don't). Surfaced in audit log
	// previews and approval-request UI.
	DisplayName string

	// Token is the verified credential the principal presented. Held
	// here so downstream layers (PDP, audit) can include it in
	// requests without re-extracting from gRPC metadata. Format is
	// opaque to most callers — the PDP knows whether it's JWT,
	// Biscuit, or something else.
	Token string

	// ExpiresAt is when the credential expires. Independent of the
	// principal's lifetime in saas-starter (a service principal can
	// live forever; this token might be 15 minutes old). Code that
	// caches Principals MUST honor this — refusing the cache hit
	// past expiry is what prevents stale-credential authority.
	ExpiresAt time.Time

	// DelegationChain records the principals whose authority is
	// being acted on, oldest-first. Empty for non-delegated calls;
	// length 1+ when authority was lent. The actor is always the
	// last entry (or the Principal itself if the chain is empty).
	//
	// Example: User U invokes Agent A; A acquires escalation from
	// User V to merge a PR. The chain on the resulting tool call is
	// [U, V] — A's own ID is the Principal.ID; the chain shows whose
	// authority is in play. Audit logs the full chain verbatim.
	DelegationChain []DelegationLink
}

// DelegationLink is one node in a chain of authority lending. Carries
// enough to audit who-lent-what without requiring a roundtrip to
// saas-starter to resolve.
type DelegationLink struct {
	// PrincipalID is the lender at this link.
	PrincipalID string

	// Kind mirrors Principal.Kind for the lender.
	Kind string

	// DisplayName mirrors Principal.DisplayName for the lender.
	// Surfaced in audit + approval UI; not authoritative for auth.
	DisplayName string

	// GrantID is the saas-starter delegation_grants.id row that
	// authorized this hop. Lets auditors trace from a tool call back
	// to the exact grant that allowed it. Empty if this link
	// pre-dates the delegation_grants table (legacy delegation).
	GrantID string
}

// PrincipalKind values. Centralized as constants so callers don't
// drift on string literals.
const (
	KindHuman   = "human"
	KindService = "service"
	KindAgent   = "agent"
)

// ErrPrincipalInvalid is the umbrella error for Validate failures.
// Wrap with %w so callers can errors.Is for the umbrella but still
// surface the specific reason.
var ErrPrincipalInvalid = errors.New("principal: invalid")

// Validate asserts the structural minimum every Principal must satisfy.
// Does NOT verify the token signature — that's the credential
// verifier's job, run before Principal is constructed. This is the
// "did upstream verification fill in the right shape" check.
//
// Returns wrapped ErrPrincipalInvalid; the wrapped message identifies
// the specific field at fault for log / error surface.
func (p *Principal) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: nil principal", ErrPrincipalInvalid)
	}
	if p.ID == "" {
		return fmt.Errorf("%w: empty ID", ErrPrincipalInvalid)
	}
	switch p.Kind {
	case KindHuman:
		// Humans are cross-org global identities (matches saas-starter's
		// principals_org_scope CHECK constraint). OrgID is OPTIONAL —
		// when set, it represents "the org context this principal is
		// currently operating in", which is useful for audit attribution
		// but not part of identity.
	case KindService, KindAgent:
		if p.OrgID == "" {
			return fmt.Errorf("%w: %s kind requires OrgID (saas-starter principals_org_scope)", ErrPrincipalInvalid, p.Kind)
		}
	case "":
		return fmt.Errorf("%w: empty Kind", ErrPrincipalInvalid)
	default:
		return fmt.Errorf("%w: unknown Kind %q (want human|service|agent)", ErrPrincipalInvalid, p.Kind)
	}
	if p.Kind == KindAgent && p.AgentID == "" {
		return fmt.Errorf("%w: agent kind requires non-empty AgentID", ErrPrincipalInvalid)
	}
	if p.Kind != KindAgent && p.AgentID != "" {
		return fmt.Errorf("%w: AgentID set on non-agent kind %q", ErrPrincipalInvalid, p.Kind)
	}
	return nil
}

// IsExpired reports whether the credential has expired as of now.
// Treats zero-time as never-expires (e.g. a permanently-issued
// service principal credential whose expiry is managed externally).
//
// Callers should check this BEFORE using the Principal for any
// auth-bearing call. The PDP also checks; this is an early-out for
// hot paths.
func (p *Principal) IsExpired() bool {
	return p.IsExpiredAt(time.Now())
}

// IsExpiredAt is IsExpired against an explicit clock — used by tests
// to avoid time.Now flakiness and by future cache implementations
// that already hold a clock.
func (p *Principal) IsExpiredAt(now time.Time) bool {
	if p == nil {
		return true
	}
	if p.ExpiresAt.IsZero() {
		return false
	}
	return !now.Before(p.ExpiresAt)
}

// AsIdentity converts the Principal to the free-form Identity map
// that PDPRequest carries. Centralized so the key shape stays
// consistent across every call site — drift here means the PDP
// can't reliably look up principal_id and falls back to default-deny.
//
// Keys (stable; do not rename):
//
//   - principal_id, principal_kind, principal_org_id
//   - agent_id (only set when Kind=agent)
//   - delegation_chain (slice of DelegationLink-as-map)
//   - principal_token (the raw credential, for downstream PDP
//     verification of signature + caveats)
//
// Unknown extra keys are tolerated — callers can add their own
// (e.g. an HTTP request ID) without breaking anything.
func (p *Principal) AsIdentity() map[string]any {
	if p == nil {
		return nil
	}
	out := map[string]any{
		"principal_id":     p.ID,
		"principal_kind":   p.Kind,
		"principal_org_id": p.OrgID,
	}
	if p.AgentID != "" {
		out["agent_id"] = p.AgentID
	}
	if p.DisplayName != "" {
		out["principal_display_name"] = p.DisplayName
	}
	if p.Token != "" {
		out["principal_token"] = p.Token
	}
	if len(p.DelegationChain) > 0 {
		chain := make([]map[string]any, 0, len(p.DelegationChain))
		for _, link := range p.DelegationChain {
			chain = append(chain, map[string]any{
				"principal_id": link.PrincipalID,
				"kind":         link.Kind,
				"display_name": link.DisplayName,
				"grant_id":     link.GrantID,
			})
		}
		out["delegation_chain"] = chain
	}
	return out
}

// principalCtxKey is unexported so external code can't accidentally
// stuff a non-Principal into the slot or read it without the helpers.
type principalCtxKey struct{}

// WithPrincipal returns a context carrying p. Use after credential
// verification — interceptors do this once per call, then handlers
// use Get to read.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFrom retrieves the Principal stamped on ctx, or nil if
// none was set. nil is the meaningful zero — callers (PDP, audit)
// branch on "no principal" as a distinct policy state, not an error.
func PrincipalFrom(ctx context.Context) *Principal {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(principalCtxKey{}).(*Principal)
	return p
}
