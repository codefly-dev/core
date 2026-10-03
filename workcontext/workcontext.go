// Package workcontext mints and verifies the signed Work Context capability
// that codefly.base.v0.WorkContextV1 describes. It is the canonical identity
// and authority carrier: every other identity shape in core is derived from a
// verified capability rather than assembled beside one.
//
// Three operations produce a capability, and none of them mutates an existing
// one:
//
//   - Start mints the first session of a task, on the owner's authority.
//   - Child exchanges a verified session for a delegated one. The child's
//     scopes must attenuate the parent's and its expiry never extends past it.
//   - Grant mints the single-use capability an approval justifies. It is the
//     one hop that may hold authority the previous hop did not, and it is
//     bound to one audience, one subject and one call so the added authority
//     is unusable anywhere else.
//
// Verify is the only way to turn a token back into claims. A capability that
// fails signature, window, audience, attenuation, grant, revision or replay
// checks yields nothing — there is no partially-trusted form.
//
// See docs/work-context.md for the model and the resume contract.
package workcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// Typ and Algorithm are the fixed header values of a v1 capability. The
// schema constrains them; restating them here is what lets a minter fill
// them without reaching into the descriptor.
const (
	Typ       = "codefly.work-context/v1"
	Algorithm = "Ed25519"
)

// Replay policies. A capability is either replayable within its window or
// consumable exactly once; there is no third answer, and a grant capability
// is always the second.
const (
	ReplayIdempotent = "idempotent"
	ReplaySingleUse  = "single-use"
)

// DefaultSkew is the clock tolerance a Verifier applies to the capability's
// window when it is not given one.
const DefaultSkew = 30 * time.Second

// MaxTokenSize bounds a presented token before anything is decoded.
//
// It is declared here because a consumer had invented its own bound when core
// declared none — which is one more rule kept in sync by hand, and the shape
// that produced the two-encodings failure. A capability is claims, not a
// payload: the largest thing in one is the actor chain, and 32 KiB is far more
// than any real chain needs while still refusing a decode bomb outright.
const MaxTokenSize = 32 << 10

// DefaultMaxTTL is the longest window Authority will mint when none is set.
//
// A ceiling exists because the minter had none: Start checked only that the
// TTL was positive, so a misconfigured host could mint a capability valid for
// a month and every verifier in the fleet would accept it for a month. The
// revocation levers reach it, but they are levers somebody has to pull, and a
// credential's own expiry is the one bound that needs nobody.
//
// One hour is long enough for a session that does real work and short enough
// that a misconfiguration is bounded by lunch rather than by a quarter. It is
// a default rather than a constant so a host with a longer legitimate need
// states it in its own configuration, where a reviewer sees it — the same
// reasoning that made the authorization revision a source rather than a
// number.
const DefaultMaxTTL = time.Hour

// MaxTTLCeiling is the longest window ANY authority will mint, whatever its
// MaxTTL says.
//
// DefaultMaxTTL alone was a default and not a bound: MaxTTL honoured any
// positive value, so a host could set thirty days and nothing refused the
// result — not the minter, which took the number as given, and not the
// verifier, which bounds no lifetime at all. A consumer measured exactly that
// and declined my advice to drop its own client-side ceiling, which was the
// right call: between a host that raises MaxTTL and a process holding the
// credential, there was nothing.
//
// Twenty-four hours because it has to be longer than any legitimate session
// and short enough that a misconfiguration is bounded by a day. A deployment
// that genuinely needs longer is not configuring a credential any more; it
// wants a different mechanism, and should have to say so in a PR against this
// constant rather than in a field.
const MaxTTLCeiling = 24 * time.Hour

// ErrInvalid is the umbrella for every rejection that is a property of the
// capability itself — signature, window, audience, attenuation, grant shape.
var ErrInvalid = errors.New("work context: invalid")

// ErrNotACoreToken is returned for a token whose payload is not this
// encoding at all — most usefully, one carrying a JSON payload.
//
// It exists because of a specific failure that cost real time. A second
// implementation of this capability once signed a hand-written JSON payload
// instead of the deterministic proto encoding. Both forms are
// "<base64url payload>.<base64url signature>" with an Ed25519 signature, so a
// token from one looked structurally fine to the other and then failed
// SIGNATURE verification — and "signature does not verify under key X" reads
// like a key-rotation or trust-root problem, which is what everyone went and
// investigated.
//
// So a foreign encoding is refused BEFORE the signature is checked, and with
// its own error. The verifier never reports a signature failure for a token
// that was never in this format: the two diagnoses are different and must not
// be reachable from the same message. A caller seeing this knows the sender is
// speaking another format, not that a key is wrong.
var ErrNotACoreToken = errors.New("work context: not a core token")

// ErrReplayed is returned when a single-use capability is presented a second
// time. Distinct from ErrInvalid: the capability is otherwise sound, and a
// caller that sees this has hit the resume contract rather than a forgery.
var ErrReplayed = errors.New("work context: already consumed")

// ErrRevoked is returned when the issuer's authorization revision has moved
// past the one the capability was minted against. This is how revoking a
// grant stops an unused grant capability.
var ErrRevoked = errors.New("work context: authorization revision superseded")

// Verified is a capability that passed every check. The claims are safe to
// read; nothing else in core reads WorkContextV1 fields off the wire.
//
// Its fields are unexported and it has no exported constructor, so Verify is
// the only way to obtain one. That is what makes "derived from a verified
// Work Context" a property the compiler holds rather than a convention: a
// caller outside this package cannot assemble a value that claims to have
// passed signature, window, audience, attenuation, grant and replay checks.
type Verified struct {
	context *basev0.WorkContextV1
	encoded string
	sha256  string
}

// Context is the verified claims, as a DEEP COPY.
//
// It used to hand out the internal pointer, which made verification real and
// the verified claims mutable. Measured:
//
//	seals.PutEpoch(owner, 3)              // revoke the owner
//	verifier.Verify(ctx, token)           // ErrRevoked, correctly
//	held.Context().Seal.PrincipalEpoch = 3 // edit the claims in place
//	verifier.Recheck(ctx, held)           // nil
//	authority.Child(ctx, held, ...)       // derives a VERIFYING child
//
// Every live check in this package reads the claims back out of the Verified
// it was handed, so a caller able to edit them could move the capability onto
// any state the issuer currently holds. The signature over the ENCODED token
// was never in question; what the checks compared against was.
//
// A copy per call is the cost. It is the right one: the alternative is every
// reader remembering not to write, which is the kind of rule that holds until
// somebody normalises a field.
func (v *Verified) Context() *basev0.WorkContextV1 {
	return proto.Clone(v.context).(*basev0.WorkContextV1)
}

// claims is the internal, un-copied view, for this package's own checks. It
// never leaves the package.
func (v *Verified) claims() *basev0.WorkContextV1 { return v.context }

// Encoded is the token exactly as presented.
func (v *Verified) Encoded() string { return v.encoded }

// SHA256 is the hex digest of the token, which is what an execution receipt
// binds its claims snapshot to.
func (v *Verified) SHA256() string { return v.sha256 }

// Fingerprint is the digest Verify records for a token, exposed so a caller
// holding only the encoded form can match it against a stored one.
func Fingerprint(encoded string) string {
	digest := sha256.Sum256([]byte(encoded))
	return hex.EncodeToString(digest[:])
}

// Actor is the current actor's hop as a DEEP COPY, or nil when the owner acts
// directly. See Context for why it copies.
func (v *Verified) Actor() *basev0.WorkActorV1 {
	actor := v.actor()
	if actor == nil {
		return nil
	}
	return proto.Clone(actor).(*basev0.WorkActorV1)
}

// actor is the internal, un-copied view.
func (v *Verified) actor() *basev0.WorkActorV1 {
	chain := v.context.GetActorChain()
	if len(chain) == 0 {
		return nil
	}
	return chain[len(chain)-1]
}

// EffectiveScopes are the scopes the current actor holds, as DEEP COPIES: the
// last hop's, or the owner's delegated authority when no hop has narrowed it.
func (v *Verified) EffectiveScopes() []*basev0.WorkScopeV1 {
	scopes := v.context.GetAuthorityScopes()
	if actor := v.actor(); actor != nil {
		scopes = actor.GetGrantedScopes()
	}
	return cloneScopes(scopes)
}

// RequireBinding asserts that this capability exercises one named operation
// binding, and refuses when it carries none or carries another.
//
// It exists because the binding half of the revocation predicate was
// unreachable at the use site. A verifier checks the sealed binding against
// the issuer's live record — revision, incarnation, withdrawal, and that it is
// granted to the exercising principal within the sealed installation — but
// only when the capability carries one. A capability carrying the same
// authority scopes and NO binding passed every check, so revoking the binding
// did not reach it.
//
// Core cannot close that by requiring a binding on every capability: a session
// carries the owner's delegated authority and a binding gates one OPERATION,
// so making it mandatory would collapse the two forms into one and force every
// session to name a unit of authority it does not exercise. What core can do
// is let the place that knows say so. A handler for an operation that is
// gated by binding X calls this, and a capability without X is refused there —
// at the use site, which is the only place that knows X.
//
// It is deliberately a method on *Verified rather than a Verifier option: the
// requirement belongs to the CALL, not to the trust boundary, and two
// operations behind one verifier legitimately require different bindings.
func (v *Verified) RequireBinding(id string) error {
	if v == nil {
		return fmt.Errorf("%w: no verified work context", ErrInvalid)
	}
	if id == "" {
		return fmt.Errorf("%w: RequireBinding needs the binding id the call is gated by", ErrInvalid)
	}
	binding := v.context.GetOperationBinding()
	if binding == nil {
		return fmt.Errorf("%w: this call is gated by operation binding %q and the capability exercises none, so revoking that binding would not reach it",
			ErrInvalid, id)
	}
	if binding.GetBindingId() != id {
		return fmt.Errorf("%w: this call is gated by operation binding %q and the capability exercises %q",
			ErrInvalid, id, binding.GetBindingId())
	}
	return nil
}

// OperationBindingID is the binding this capability exercises, or "" when it
// exercises none. A caller comparing it by hand is writing RequireBinding
// badly; it is exported for logging and audit, not for gating.
func (v *Verified) OperationBindingID() string {
	return v.context.GetOperationBinding().GetBindingId()
}
