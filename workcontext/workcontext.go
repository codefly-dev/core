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
	"time"

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

// Context is the verified claims snapshot.
func (v *Verified) Context() *basev0.WorkContextV1 { return v.context }

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

// Actor is the current actor's hop, or nil when the owner acts directly.
func (v *Verified) Actor() *basev0.WorkActorV1 {
	chain := v.context.GetActorChain()
	if len(chain) == 0 {
		return nil
	}
	return chain[len(chain)-1]
}

// EffectiveScopes are the scopes the current actor holds: the last hop's, or
// the owner's delegated authority when no hop has narrowed it.
func (v *Verified) EffectiveScopes() []*basev0.WorkScopeV1 {
	if actor := v.Actor(); actor != nil {
		return actor.GetGrantedScopes()
	}
	return v.context.GetAuthorityScopes()
}
