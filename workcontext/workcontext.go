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

// ErrInvalid is the umbrella for every rejection that is a property of the
// capability itself — signature, window, audience, attenuation, grant shape.
var ErrInvalid = errors.New("work context: invalid")

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
type Verified struct {
	// Context is the verified claims snapshot.
	Context *basev0.WorkContextV1

	// Encoded is the token exactly as presented.
	Encoded string

	// SHA256 is the hex digest of Encoded, which is what an execution
	// receipt binds its claims snapshot to.
	SHA256 string
}

// Actor is the current actor's hop, or nil when the owner acts directly.
func (v *Verified) Actor() *basev0.WorkActorV1 {
	chain := v.Context.GetActorChain()
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
	return v.Context.GetAuthorityScopes()
}
