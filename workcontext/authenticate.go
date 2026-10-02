package workcontext

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// ErrNeedsIssuer is returned when a check can only be answered by the party
// that keeps the issuer's own records, and the entrypoint in use does not.
//
// Today that is one check: the approval a grant capability claims. A verifier
// holds a GrantSource so a signed capability cannot assert an approval nobody
// recorded; an Authenticator has none, so it REFUSES a capability carrying a
// grant hop rather than accepting one whose approval went unchecked.
//
// It is its own sentinel because the two situations it sits between read
// alike and are not alike: "this capability is not a credential" and "this
// entrypoint cannot answer whether it is". A caller seeing this learns that
// the capability must be presented to the party that holds the grant records,
// which is a routing fact, not a refusal of the caller.
var ErrNeedsIssuer = errors.New("work context: this check needs the issuer's own records")

// Authenticated is a capability that passed every check an Authenticator
// makes. The claims are safe to read.
//
// It is a DIFFERENT TYPE from Verified, and that is the whole of its design.
// Both are produced by the same code — Authenticate runs (*Verifier).Verify —
// so neither is weaker on any check it performs. What differs is which
// question was answered: a Verified capability had its approval hop held
// against the issuer's record of the approval, and an Authenticated one was
// refused if it carried an approval hop at all. The type system carries that
// difference so a caller cannot lose it:
//
//   - policy.PrincipalFromWorkContext takes a *Verified, so an Authenticated
//     capability cannot become a Principal by accident.
//   - Authority.Child and Authority.Grant take a *Verified, so an
//     Authenticated capability cannot be exchanged for a derived one. That is
//     right for a reason beyond types: deriving holds the parent's inherited
//     seal against live state, which is the issuer's to answer.
//
// There is deliberately no conversion in either direction, and a test guards
// against one being added. A function turning an Authenticated into a
// Verified would be the silent downgrade this type exists to make
// unreachable: one call site, no diff anywhere else, and every rule
// downstream now resting on a question nobody asked.
type Authenticated struct {
	verified *Verified
}

// Context is the authenticated claims snapshot.
func (a *Authenticated) Context() *basev0.WorkContextV1 { return a.verified.Context() }

// Encoded is the token exactly as presented.
func (a *Authenticated) Encoded() string { return a.verified.Encoded() }

// SHA256 is the hex digest of the token.
func (a *Authenticated) SHA256() string { return a.verified.SHA256() }

// Actor is the current actor's hop, or nil when the owner acts directly.
func (a *Authenticated) Actor() *basev0.WorkActorV1 { return a.verified.Actor() }

// EffectiveScopes are the scopes the current actor holds.
func (a *Authenticated) EffectiveScopes() []*basev0.WorkScopeV1 { return a.verified.EffectiveScopes() }

// Authenticator is the verify-only entrypoint: it authenticates an incoming
// capability for a party that holds the issuer's live sealed state but does
// not mint and does not keep the approvals engine's records — the host's
// gateway.
//
// # Two entrypoints, one implementation, one strength
//
// Authenticate does not re-check anything itself. It assembles a Verifier and
// calls Verify, so every rule is applied by the same code in the same order,
// and a change to a rule reaches both entrypoints or neither. The kit proves
// it: conformance.RunAuthenticator drives all of core's fixtures and requires
// the SAME outcome and the SAME named reason as the full verifier reaches,
// fixture for fixture.
//
// EXACTLY ONE of a Verifier's four sources is not a field here, and its
// absence is a refusal rather than a check that stops happening:
//
//   - Grants is gone. A capability carrying a grant hop is refused with
//     ErrNeedsIssuer. It is not accepted-without-checking, which is what a
//     weaker entrypoint would have done and what the kit now catches.
//
// Seals and Revisions are still required, and that is deliberate. Sealed state
// is not the issuer's bookkeeping — it is the live binding of the presented
// capability to one installation, one epoch, one build and one operation
// binding, and it is what makes every revocation in this model reach a
// capability already in flight. A caller that holds neither cannot
// authenticate at full strength, and core does not offer it a way to appear
// to: there is no mode in which the seal comparison is skipped, or in which
// the authorization revision is assumed, by any entrypoint.
//
// So this type is a Verifier minus one source, and that is the whole of the
// difference. An earlier draft also replaced Revisions with a stated number;
// see that field for why a consumer was right to refuse it.
type Authenticator struct {
	// Issuer is the authority this authenticator trusts.
	Issuer string

	// Audience is this service or trust boundary.
	Audience string

	// Keys are the issuer's public keys by key id.
	Keys map[string]ed25519.PublicKey

	// Seals answers the live installation, principal-epoch, build and
	// operation-binding state the capability's seal is compared against.
	// Required: see the type comment for why there is no seal-less mode.
	Seals SealSource

	// Revisions answers the issuer's current authorization revision, which is
	// how the coarse revocation lever reaches a capability already in flight.
	// Required, and the same interface a Verifier takes.
	//
	// An earlier version of this type had a plain uint64 here instead, on the
	// argument that a party which does not mint cannot read the issuer's
	// revision live, so a stated number put that limit where a reviewer sees
	// it. That was wrong, and module-saas-starter#953 found it with the
	// evidence: RevisionSource is per TENANT by its own signature, so one
	// stated number against a multi-tenant issuer either refuses every tenant
	// not at that number, or — the dangerous one — goes on accepting
	// capabilities minted against a SUPERSEDED revision for every tenant
	// except the one it happens to name. The field read as if revocation were
	// enforced while enforcing it for at most one tenant, which is precisely
	// the class of defect this package exists to remove.
	//
	// A single-tenant caller supplies FixedRevision, which is three visible
	// characters more and says in its own documentation that it answers one
	// number for every tenant. That is a better place for the posture to be
	// legible than a bare field, and it keeps one definition of "the issuer's
	// current authorization revision" rather than two shapes that disagree.
	Revisions RevisionSource

	// Replay consumes single-use capabilities. Required: single-use is a
	// property of the verifier, not of the token, and more than one process
	// verifying against separate stores is not single-use at all.
	Replay ReplayStore

	// Now is the clock, for tests. nil means time.Now.
	Now func() time.Time

	// Skew is the tolerance applied to the capability's window. Zero means
	// DefaultSkew.
	Skew time.Duration
}

// Authenticate checks a presented capability and consumes it when it is
// single-use, exactly as Verify does, and returns the claims as an
// Authenticated rather than a Verified.
//
// A capability carrying a grant hop is refused with ErrNeedsIssuer: the
// approval it claims is a record only the issuer holds, and this entrypoint
// does not get to decide that an unchecked approval is good enough.
func (a *Authenticator) Authenticate(ctx context.Context, encoded string) (*Authenticated, error) {
	verifier, err := a.verifier()
	if err != nil {
		return nil, err
	}
	verified, err := verifier.Verify(ctx, encoded)
	if err != nil {
		return nil, err
	}
	return &Authenticated{verified: verified}, nil
}

// verifier is the one check path, assembled from this authenticator's inputs.
// Nothing else in this file compares, decodes or refuses anything.
func (a *Authenticator) verifier() (*Verifier, error) {
	switch {
	case a.Issuer == "":
		return nil, fmt.Errorf("work context: authenticator names no issuer")
	case a.Audience == "":
		return nil, fmt.Errorf("work context: authenticator names no audience")
	case len(a.Keys) == 0:
		return nil, fmt.Errorf("work context: authenticator holds no verification key")
	case a.Seals == nil:
		return nil, fmt.Errorf("work context: authenticator is missing a seal source; there is no mode in which the seal comparison is skipped")
	case a.Replay == nil:
		return nil, fmt.Errorf("work context: authenticator is missing a replay store; single-use is a property of the verifier")
	case a.Revisions == nil:
		return nil, fmt.Errorf("work context: authenticator is missing a revision source; the authorization revision is per tenant, so there is no single number to default to")
	}
	return &Verifier{
		Issuer:    a.Issuer,
		Audience:  a.Audience,
		Keys:      a.Keys,
		Revisions: a.Revisions,
		Replay:    a.Replay,
		Grants:    issuerOnlyGrants{},
		Seals:     a.Seals,
		Now:       a.Now,
		Skew:      a.Skew,
	}, nil
}

// issuerOnlyGrants is the GrantSource of a party that keeps no grant records:
// every lookup is ErrNeedsIssuer, so a capability carrying a grant hop is
// refused.
//
// Refusing is the only safe answer. Returning a synthesised grant that
// matched whatever the capability claimed would make the hop
// self-authorizing, which is the failure the GrantSource exists to prevent —
// and it would be reached by a verifier that looked entirely correct.
type issuerOnlyGrants struct{}

func (issuerOnlyGrants) Grant(_ context.Context, grantID string) (*Grant, error) {
	return nil, fmt.Errorf("%w: grant %q is an approval the issuer recorded, so this capability must be presented to the party that holds those records",
		ErrNeedsIssuer, grantID)
}
