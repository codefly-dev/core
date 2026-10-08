package workcontext

import (
	"crypto/ed25519"
	"fmt"
	"time"
)

// Inspector is the FORWARDING HOP's entrypoint: the host's gateway, or any
// proxy that routes a capability to its callee, checking what it can check
// before it forwards — and consuming nothing on the way.
//
// # Why a hop cannot verify
//
// Verify is a callee's operation. It needs the issuer's live state (a revision
// source, a seal source, a grant source), and it CONSUMES a single-use
// capability's nonce as its last step. A gateway that verified therefore
// burned the nonce in flight, and the callee — the one consumer the capability
// was minted for — refused it as replayed; a gateway that did not verify
// forwarded blind. A host measured exactly that and kept a hand-written
// signature check at its edge, which is a second implementation of the wire
// contract, because core offered it nothing else. The edge holds a trust root
// and a routing table and nothing more, so this is the entrypoint shaped like
// that: one trust root, one check per presentation, no state, no consumption.
//
// # One body, not one more
//
// Inspect does not re-check anything itself. It assembles a Verifier shell
// from the trust root and the route and runs (*Verifier).inspect — the exact
// prefix Verify runs before it reaches for live state: the decode path (shape,
// encoding, schema, the lifetime bound, no unknown field, the canonical
// encoding), the issuer pin, the signature under the key the capability names,
// the audience, the window, the chain's attenuation and the grant hop's
// shape. The kit's RunInspector holds it to Verify's outcome and Verify's
// MESSAGE on every fixture a hop can see, so a rule reaching one entrypoint
// reaches both or neither.
//
// # What it deliberately does NOT do
//
//   - It consults no live state. The authorization revision, the sealed
//     installation's revision, every principal's epoch, the approved build,
//     the operation binding's withdrawal and the issuer's record of a grant
//     are the callee's to hold a capability against. A hop that refused on any
//     of them would be claiming a check it cannot make, and one that answered
//     them from a cache would be a second verifier with stale state. So a
//     capability the issuer has revoked INSPECTS and is FORWARDED, and the
//     callee refuses it; the kit requires exactly that.
//   - It consumes nothing. There is no replay store to consume with and no
//     nonce is read. A single-use capability inspects every time it is
//     presented, and the callee burns it exactly once. One consumer per
//     capability: the hop inspects, the callee verifies.
//   - It grants nothing and derives nothing. The result carries no principal,
//     no actor, no scope, no grant hop and no binding — only what a hop routes
//     on — and no function in this package or in policy turns it into a
//     Verified, an Authenticated or a Principal. A hop routes; it never acts.
//   - It judges no approval. A grant capability is forwarded like any other,
//     to the callee holding the approval records. Authenticator refuses one
//     with ErrNeedsIssuer because an Authenticator ACTS on what it accepts; a
//     hop does not, so for a hop "forward it" is the deferral.
//
// # The route target is the hop's, never the token's
//
// The audience is a per-call argument rather than a field because a hop
// serves many routes, and because of where the value must come from: the hop
// resolves the target from the request it is routing — the path, the host,
// its routing table — and asks whether the capability is addressed THERE.
// Reading the audience off the token and comparing it to itself passes
// everything; so does an empty expectation treated as a wildcard. A host
// found the second one in its own gateway, as an empty-string sentinel
// meaning "do not check". Here an empty route target is refused outright.
type Inspector struct {
	// Issuer is the authority this hop trusts. A capability from any other
	// issuer is refused even when its signature checks out against a key this
	// hop holds: the key is not the trust decision.
	Issuer string

	// Keys are the issuer's public keys by key id — the hop's JWKS cache, in
	// the type every entrypoint takes.
	Keys map[string]ed25519.PublicKey

	// Now is the clock, for tests. nil means time.Now.
	Now func() time.Time

	// Skew is the tolerance applied to the capability's window. Zero means
	// DefaultSkew.
	Skew time.Duration

	// TrustTheConformanceFixtureKey is Verifier's field of the same name, and
	// exists here for the same reason: the kit's key is derivable from this
	// package's source by anyone, so a hop refuses it unless a line somebody
	// wrote says otherwise. Only the conformance kit sets it.
	TrustTheConformanceFixtureKey bool
}

// Inspected is what a forwarding hop learned about a capability it is about
// to forward: that it is this encoding, signed by the issuer the hop trusts
// under a key the hop holds, inside its window, addressed to the route the hop
// resolved, and shaped like a sealed capability.
//
// It is a distinct type from Verified and Authenticated, and that is the whole
// of its safety. Those two carry claims a callee acts on, and every function
// that derives identity or mints takes one of them. This carries ONLY what a
// hop routes on, and nothing takes it. The claims that make a capability an
// authority — the owner, the actor chain, the scopes, the grant hop, the
// operation binding, the nonce — are not here, not because they are secret
// (anyone holding the token can decode them) but because a hop has no use for
// them that is not acting, and a type that cannot express acting is a hop
// that cannot be turned into a callee by accident.
//
// Nothing here is for stamping onto the forwarded request. The callee verifies
// the token itself; a header a hop writes is a header a caller can write, and
// a host already paid for learning that once.
type Inspected struct {
	audience     string
	tenant       string
	installation string
	task         string
	session      string
}

// Audience is the route target the capability is addressed to — equal to the
// one the hop asked about, echoed so a log line can name it.
func (i *Inspected) Audience() string {
	if i == nil {
		return ""
	}
	return i.audience
}

// TenantID is the tenant the work belongs to, for a hop that routes by tenant.
func (i *Inspected) TenantID() string {
	if i == nil {
		return ""
	}
	return i.tenant
}

// InstallationID is the installation the capability is sealed to, for a hop
// that routes to an installation's own backend.
func (i *Inspected) InstallationID() string {
	if i == nil {
		return ""
	}
	return i.installation
}

// TaskID is the task the capability belongs to, for correlation.
func (i *Inspected) TaskID() string {
	if i == nil {
		return ""
	}
	return i.task
}

// SessionID is the session the capability belongs to, for correlation and
// session affinity.
func (i *Inspected) SessionID() string {
	if i == nil {
		return ""
	}
	return i.session
}

// Inspect checks a presented capability against this hop's trust root, the
// clock and the route target the hop resolved, and returns the claims a hop
// may route on. It consumes nothing: present the same token a thousand times
// and it inspects a thousand times, and the callee's Verify still finds the
// nonce intact.
//
// audience is the route target THE HOP resolved from the request it is
// forwarding — never a value read off the token. An empty one is refused:
// "no expectation" is not "any audience", and a hop with no route has nothing
// to forward to.
//
// A nil error means the capability may be forwarded to that route. It does
// not mean the capability is live — the callee decides that against the
// issuer's state — and a hop that acts on the result instead of forwarding has
// made itself a callee without a callee's inputs.
func (i *Inspector) Inspect(token, audience string) (*Inspected, error) {
	switch {
	case i.Issuer == "":
		return nil, fmt.Errorf("work context: inspector names no issuer")
	case len(i.Keys) == 0:
		return nil, fmt.Errorf("work context: inspector holds no verification key")
	case audience == "":
		return nil, fmt.Errorf("work context: inspect needs the route target the hop resolved, and was given none; an empty expectation is not a wildcard")
	}
	// A Verifier SHELL: the trust root, the route and the clock, and none of
	// the four sources. Only the stateless prefix is run on it; the sources
	// are nil because a hop holds none, and nothing on this path reads them.
	shell := &Verifier{
		Issuer:                        i.Issuer,
		Audience:                      audience,
		Keys:                          i.Keys,
		Now:                           i.Now,
		Skew:                          i.Skew,
		TrustTheConformanceFixtureKey: i.TrustTheConformanceFixtureKey,
	}
	wc, _, err := shell.inspect(token)
	if err != nil {
		return nil, err
	}
	return &Inspected{
		audience:     wc.GetAudience(),
		tenant:       wc.GetTenantId(),
		installation: wc.GetSeal().GetInstallationId(),
		task:         wc.GetTaskId(),
		session:      wc.GetSessionId(),
	}, nil
}
