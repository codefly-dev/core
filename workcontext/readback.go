package workcontext

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// ReadBack is the issuer answering a question about a capability it issued,
// at full strength and WITHOUT SPENDING IT.
//
// The question it exists for: a capability's holder asks the issuer something
// about it — "may this capability read record R?", "which collections does it
// reach?", "is this parent still good?" — and presents the capability as the
// subject of the question. Verify cannot answer that. It consumes a single-use
// capability, so the holder that verified it first (the consumer, which is
// what single-use is for) could no longer ask, and a paginated question died
// on its second page. Recheck cannot answer it either: it needs a *Verified,
// which only the consumer holds.
//
// WHO MAY CALL IT is decided by the receiver, not by a comment. It is a method
// on Authority, and an Authority signs: building one takes the issuer's
// private key or signer. A consumer holds public keys and nothing else, so it
// has no way to reach this — it has Verify, which consumes, and Inspect and
// Authenticate, which are the hop's and the verify-only consumer's answers.
//
// WHY IT IS NOT A REPLAY HOLE. Single-use bounds how many times a capability
// can be PRESENTED AS A CREDENTIAL — the consumer acts on it once. A read-back
// is not a presentation: the issuer is reading its own signed statement back,
// on the holder's behalf, to answer a question, and the answer authorizes
// nothing. That is held by the type. A ReadBackCapability is not a *Verified
// and cannot become one, so it cannot be passed to Child, Grant or Reissue to derive a
// capability, nor to Recheck — the only things core lets a verified capability
// do. What the caller does with the answer is the caller's: an issuer that
// performed the capability's effect itself on the strength of a read-back
// would have used the capability without consuming it, and that is a misuse of
// this method, not a gap in it.
//
// WHAT IT CHECKS is everything Verify checks, by the same body: the encoding,
// the issuer, the signature under THIS authority's key, the audience the
// caller names, the window, the structure, the authorization revision, the
// seal and every actor hop's epoch and execution, the operation binding, and
// the issuer's record of a grant. A capability Verify would refuse for any
// reason but replay, this refuses with the same error. The only difference is
// that the replay store is never read or written: a single-use capability
// already consumed by its consumer can still be asked about, and asking does
// not consume it.
//
// The audience is the caller's to name, as for any verifier: the issuer reads
// a capability back for the audience it was minted for, and one minted for
// another audience is refused rather than answered about.
func (a *Authority) ReadBack(ctx context.Context, encoded string, check ReadBackCheck) (*ReadBackCapability, error) {
	if a.Revisions == nil || a.Seals == nil || check.Grants == nil {
		return nil, fmt.Errorf("work context: read-back is missing a revision source, seal source or grant source")
	}
	if check.Audience == "" {
		return nil, fmt.Errorf("work context: read-back names no audience")
	}
	signer, err := a.signingKey()
	if err != nil {
		return nil, fmt.Errorf("work context: read-back: %w", err)
	}
	public, ok := signer.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("work context: read-back: the authority's public key is not an ed25519 key")
	}
	// The issuer's own key, under the key id it signs with, and the issuer's
	// own live state: the verifier a consumer would build, minus the replay
	// store this question must never touch.
	v := &Verifier{
		Issuer:    a.Issuer,
		Audience:  check.Audience,
		Keys:      map[string]ed25519.PublicKey{a.KeyID: public},
		Revisions: a.Revisions,
		Grants:    check.Grants,
		Seals:     a.Seals,
		Now:       a.Now,
		Skew:      check.Skew,
	}
	wc, err := v.check(ctx, encoded, false)
	if err != nil {
		return nil, err
	}
	return &ReadBackCapability{verified: &Verified{context: wc, encoded: encoded, sha256: Fingerprint(encoded)}}, nil
}

// ReadBackCheck is what a read-back is asked: the audience the capability is
// read back for, the issuer's record of the approvals a grant capability can
// claim, and the window tolerance.
type ReadBackCheck struct {
	// Audience the capability must have been minted for. Required.
	Audience string

	// Grants resolves the approval a grant capability claims, exactly as a
	// Verifier's does. Required: an issuer that cannot read its own grant
	// records cannot say a grant capability is good.
	Grants GrantSource

	// Skew is the tolerance applied to the capability's window. Zero means
	// DefaultSkew.
	Skew time.Duration
}

// ReadBackCapability is a capability its issuer has read back. The claims are
// safe to read; nothing else is available on it.
//
// It is a DIFFERENT TYPE from Verified for the reason Authenticated is: the
// same checks produced it, but a different question was answered — this one
// was not presented and not consumed — and the type carries that so a caller
// cannot lose it. There is deliberately no conversion to a *Verified, and
// TestNoDeclarationTurnsAReadBackCapabilityIntoAVerifiedOne guards that
// direction by AST: one would let a capability be derived from, or rechecked
// as, a credential nobody presented.
type ReadBackCapability struct {
	verified *Verified
}

// Context is the claims snapshot.
func (r *ReadBackCapability) Context() *basev0.WorkContextV1 { return r.verified.Context() }

// Encoded is the token exactly as read back.
func (r *ReadBackCapability) Encoded() string { return r.verified.Encoded() }

// SHA256 is the hex digest of the token.
func (r *ReadBackCapability) SHA256() string { return r.verified.SHA256() }

// Actor is the current actor's hop, or nil when the owner acts directly.
func (r *ReadBackCapability) Actor() *basev0.WorkActorV1 { return r.verified.Actor() }

// EffectiveScopes are the scopes the current actor holds.
func (r *ReadBackCapability) EffectiveScopes() []*basev0.WorkScopeV1 {
	return r.verified.EffectiveScopes()
}
