package workcontext

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// decodeClaims turns a presented token into its claims message and the raw
// bytes a signature is checked over. It is the ONE decode path: Verify and
// Inspect both go through it, so a consumer's structural refusal and core's
// verification cannot disagree about what a token even is.
//
// That disagreement is the reason this is factored out rather than
// duplicated. A consumer hand-parsing a carried capability reached different
// sentinels than core's own fixtures declare — one answered ErrUnsealed where
// core answers ErrInvalid, and it never read the actor epochs at all. Two
// implementations of "is this a core token" is the same failure as two
// implementations of the capability, one layer down.
func decodeClaims(encoded string) (*basev0.WorkContextV1, []byte, []byte, error) {
	payload, signature, found := strings.Cut(encoded, ".")
	if !found {
		return nil, nil, nil, fmt.Errorf("%w: token is not <payload>.<signature>", ErrInvalid)
	}
	claims, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: payload is not base64url: %v", ErrInvalid, err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: signature is not base64url: %v", ErrInvalid, err)
	}
	// Before anything is unmarshalled and well before the signature is
	// checked: is this even this encoding? See ErrNotACoreToken.
	if err := CheckEncoding(claims); err != nil {
		return nil, nil, nil, err
	}
	wc := &basev0.WorkContextV1{}
	if err := proto.Unmarshal(claims, wc); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: payload is not a WorkContextV1: %v", ErrInvalid, err)
	}
	if err := protovalidate.Validate(wc); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return wc, claims, sig, nil
}

// checkSealedStructure enforces what a capability must CARRY to be sealable at
// all, as distinct from whether what it carries is current. It is the half of
// the seal contract that needs no issuer state: a seal is present and names an
// installation, and every actor hop carries an epoch.
//
// Verify runs it too, so Inspect cannot accept something Verify refuses
// structurally.
func checkSealedStructure(wc *basev0.WorkContextV1) error {
	if _, err := sealOf(wc); err != nil {
		return err
	}
	for index, hop := range wc.GetActorChain() {
		if hop.PrincipalEpoch == nil {
			return fmt.Errorf("%w: actor hop %d (%s) carries no epoch, so it cannot be revoked", ErrUnsealed, index, hop.GetPrincipalId())
		}
	}
	return nil
}

// Inspect reports whether a token is STRUCTURALLY a sealed capability of this
// package, and nothing else. It returns an error only — deliberately, and the
// reason is the whole of its safety.
//
// # What it checks
//
// The shape, the encoding (ErrNotACoreToken for a foreign one), that the
// payload unmarshals and satisfies the schema, that the chain attenuates and a
// grant hop is well formed, that a seal is present and names an installation,
// and that every actor hop carries an epoch. Core's own sentinels, from core's
// own code — Verify runs the same functions.
//
// # What it does NOT check, and why there is no way to misread that
//
// No signature. No issuer. No audience. No window. No issuer state of any
// kind: not the authorization revision, not the live seal, not an epoch, not a
// binding, not a grant record. **A nil return means only that the token is
// shaped like a sealed capability. It authenticates nothing, and a caller that
// treats nil as permission has skipped verification entirely.**
//
// It returns NO CLAIMS, and that is the design rather than an omission.
// Returning them would make this a third strength: a caller would read the
// claims, act on them, and have authorized on an unverified token — with no
// diff anywhere to show it. With nothing to read, the only thing a caller can
// do with the answer is the thing it was asked for: refuse early.
//
// # What it is for
//
// A SENDER about to attach a capability to a request, asking "is this thing
// even sealed", so it refuses before the call rather than having the receiver
// refuse after. That is a different question from either verification
// entrypoint — Verify answers it for a receiver with the issuer's state,
// Authenticator for one with caller-supplied state — and it exists here
// because consumers were answering it by hand and reaching different
// sentinels than core's fixtures declare.
func Inspect(encoded string) error {
	wc, _, _, err := decodeClaims(encoded)
	if err != nil {
		return err
	}
	if err := checkStructure(wc); err != nil {
		return err
	}
	return checkSealedStructure(wc)
}

// Recheck holds an ALREADY-VERIFIED capability against the issuer's live state
// again, without consuming anything.
//
// It takes a *Verified, and that is what makes it safe to exist. A caller must
// already hold one, which means the token has passed signature, issuer,
// audience, window, attenuation, grant and — if it is single-use — its one
// legitimate replay consumption. So Recheck cannot be used as a first
// verification by anybody: there is no way to obtain the argument except by
// verifying.
//
// It re-reads exactly what can change under a long-running call: the window,
// the authorization revision, the seal, every actor hop's epoch, the operation
// binding, and the issuer's record of a grant. It does NOT re-check the
// signature — the bytes are the same bytes Verify already checked — and it
// does NOT touch the replay store.
//
// That last point is why this exists. Verify consumes a single-use nonce, so a
// stream guard that re-verified a grant capability before each emission killed
// the stream on its first check. Re-checking liveness and consuming a nonce
// are two different operations and only one of them belongs in a loop.
//
// A caller deciding HOW OFTEN to call this should decide it explicitly. Per
// emission is one round trip per emitted item against an RPC-backed
// SealSource; per stream catches a revocation only at the start. Core does not
// choose, and neither interval is wrong — but discovering the choice from a
// latency graph is.
func (v *Verifier) Recheck(ctx context.Context, verified *Verified) error {
	if verified == nil {
		return fmt.Errorf("%w: recheck needs a verified capability", ErrInvalid)
	}
	if v.Revisions == nil || v.Grants == nil || v.Seals == nil {
		return fmt.Errorf("work context: verifier is missing a revision source, grant source or seal source")
	}
	wc := verified.Context()
	now := v.now()
	skew := v.skew()
	// The window first: a stream must not outlive the capability carrying it.
	expires := time.Unix(wc.GetExpiresAtUnix(), 0)
	if !now.Add(-skew).Before(expires) {
		return fmt.Errorf("%w: expired at %s", ErrInvalid, expires.UTC().Format(time.RFC3339))
	}
	current, err := v.Revisions.AuthorizationRevision(ctx, wc.GetTenantId())
	if err != nil {
		return fmt.Errorf("work context: authorization revision for tenant %q: %w", wc.GetTenantId(), err)
	}
	if wc.GetAuthorizationRevision() < current {
		return fmt.Errorf("%w: minted at revision %d, issuer is at %d", ErrRevoked, wc.GetAuthorizationRevision(), current)
	}
	if err := v.checkSeal(ctx, wc); err != nil {
		return err
	}
	if hop := wc.GetGrantHop(); hop != nil {
		return v.checkGrant(ctx, wc, hop, now)
	}
	return nil
}
