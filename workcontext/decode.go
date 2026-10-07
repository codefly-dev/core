package workcontext

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// decodeClaims turns a presented token into its claims message and the raw
// bytes a signature is checked over. It is the ONE decode path: Verify,
// Inspect and Decode all go through it, so a consumer's structural refusal, a
// forwarding hop's check and core's verification cannot disagree about what a
// token even is.
//
// That disagreement is the reason this is factored out rather than
// duplicated. A consumer hand-parsing a carried capability reached different
// sentinels than core's own fixtures declare — one answered ErrUnsealed where
// core answers ErrInvalid, and it never read the actor epochs at all. Two
// implementations of "is this a core token" is the same failure as two
// implementations of the capability, one layer down.
func decodeClaims(encoded string) (*basev0.WorkContextV1, []byte, []byte, error) {
	// The bound belongs in the ONE decode path, not in one entrypoint. It was
	// enforced only in the structural read (Decode, then called Inspect), so a
	// caller that reached Verify directly — the strong path, and the one a
	// receiver actually uses — had no bound at all.
	if size := len(encoded); size > MaxTokenSize {
		return nil, nil, nil, fmt.Errorf("%w: token is %d bytes, over the %d-byte maximum", ErrInvalid, size, MaxTokenSize)
	}
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
	// No unknown fields, anywhere in the message.
	//
	// A capability carrying a field this Core does not know is a capability
	// some other minter produced, and accepting it is accepting a second
	// implementation at the wire — the exact thing the one-implementation rule
	// forbids, reached without anyone writing a second verifier. It also lets
	// data ride inside a signed credential that nothing here checks, reads or
	// can reason about, which a consumer downstream may well read.
	if path := unknownFieldIn(wc.ProtoReflect()); path != "" {
		return nil, nil, nil, fmt.Errorf("%w: carries an unknown field at %s; this Core does not know it, so some other minter wrote it",
			ErrInvalid, path)
	}
	// The payload must BE its own canonical encoding.
	//
	// The signature covers whatever bytes were presented, so without this a
	// second minter emitting a different-but-valid encoding of the same
	// claims passes verification and the whole conformance kit. solutionhost
	// enforces exactly this for its documents, with ErrNotCanonical; the
	// capability had no equivalent. Re-marshalling deterministically and
	// requiring byte equality is what makes "one encoding" checkable rather
	// than merely asserted.
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(wc)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("work context: re-marshal: %w", err)
	}
	if !bytes.Equal(canonical, claims) {
		return nil, nil, nil, fmt.Errorf("%w: the payload is not its own canonical encoding, so it was produced by a different encoder", ErrInvalid)
	}
	// The LIFETIME, once the claims are known to be this encoding: a window
	// wider than MaxTTLCeiling is refused whichever entrypoint asked.
	//
	// This bound was added to Verify alone, in the same commit whose comment
	// above says a bound belongs in the one decode path and not in one
	// entrypoint. The same mistake, in the same file, about a different
	// bound. What it cost is specific and worse than an unchecked path: a
	// mint client reads its own window through Decode, never verifying a
	// signature, so a thirty-day capability that every Verify refuses was
	// reported to its holder as thirty days of validity. An absent bound
	// tells a caller nothing; this one told it something false about the only
	// field it calls Decode to read.
	if lifetime := time.Unix(wc.GetExpiresAtUnix(), 0).Sub(time.Unix(wc.GetNotBeforeUnix(), 0)); lifetime > MaxTTLCeiling {
		return nil, nil, nil, fmt.Errorf("%w: the capability's lifetime is %s and no capability is accepted beyond %s",
			ErrInvalid, lifetime, MaxTTLCeiling)
	}
	return wc, claims, sig, nil
}

// unknownFieldIn reports the path of the first unknown field it finds,
// recursively, or "" when there is none. Recursion is the point: an unknown
// field nested inside a seal or an actor hop is as foreign as one at the top,
// and a check that looked only at the root would pass it.
func unknownFieldIn(message protoreflect.Message) string {
	if len(message.GetUnknown()) > 0 {
		return string(message.Descriptor().FullName())
	}
	found := ""
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		switch {
		case field.IsMap():
			if field.MapValue().Kind() != protoreflect.MessageKind {
				return true
			}
			value.Map().Range(func(key protoreflect.MapKey, entry protoreflect.Value) bool {
				if path := unknownFieldIn(entry.Message()); path != "" {
					found = string(field.FullName()) + "[" + key.String() + "]." + path
					return false
				}
				return true
			})
		case field.IsList() && field.Kind() == protoreflect.MessageKind:
			list := value.List()
			for index := 0; index < list.Len(); index++ {
				if path := unknownFieldIn(list.Get(index).Message()); path != "" {
					found = fmt.Sprintf("%s[%d].%s", field.FullName(), index, path)
					return false
				}
			}
		case !field.IsList() && field.Kind() == protoreflect.MessageKind:
			if path := unknownFieldIn(value.Message()); path != "" {
				found = string(field.FullName()) + "." + path
			}
		}
		return found == ""
	})
	return found
}

// Decoded is a token that is structurally a sealed capability of this
// package. **Nothing about it has been authenticated.**
//
// It is a distinct type from Verified, Authenticated and Inspected for the
// same reason those are distinct from each other: the type carries which
// question was answered. Verified means the issuer's state agreed.
// Authenticated means the caller's supplied state agreed. Inspected means a
// forwarding hop's trust root and route agreed. Decoded means only that the
// bytes are shaped like a sealed capability — no signature was checked and no
// state was consulted, so a forgery produces one exactly as readily as a real
// capability does, and a test asserts that.
//
// What it is therefore FOR: reading a credential you already obtained some
// other way — your own, handed to you by your issuer over an authenticated
// channel — so you can see its expiry and its seal without re-deriving them
// by hand. Reading your own credential is not an authorization decision.
//
// What it cannot do: become authority. Every function that derives identity
// or mints takes a checked capability — policy.PrincipalFromWorkContext a
// *Verified, policy.PrincipalFromAuthenticatedWorkContext an *Authenticated,
// Authority.Child and Authority.Grant a *Verified — so a Decoded capability
// can become neither a Principal nor a derived capability.
//
// There is no function turning a Decoded into any of them, and
// TestNoDeclarationTurnsAnInspectedOrDecodedCapabilityIntoAVerifiedOne guards
// that direction by AST. It guards ONE direction, and this once said "either
// direction" while the test checked one: a function handing back a Decoded in
// place of something stronger removes standing rather than granting it, so it
// needs no guard. The claim did.
//
// # It was called Inspected
//
// This type and its constructor were Inspected and Inspect until a forwarding
// hop needed an entrypoint of that name which DOES check a signature. A
// structural read called Inspect beside a signature check called Inspect is
// the two-strengths trap this package exists to make unreachable: a hop
// author reaching for the wrong one would have forwarded forgeries through a
// call that read as a check. Decode says what this does, and nothing more.
type Decoded struct {
	context *basev0.WorkContextV1
}

// Context is the claims as they were carried. They are DATA, not findings: no
// signature stands behind them.
func (d *Decoded) Context() *basev0.WorkContextV1 { return d.context }

// ExpiresAt is when the capability's window closes — the field a holder reads
// to know when to mint afresh.
func (d *Decoded) ExpiresAt() time.Time { return time.Unix(d.context.GetExpiresAtUnix(), 0) }

// NotBefore is when the window opens.
func (d *Decoded) NotBefore() time.Time { return time.Unix(d.context.GetNotBeforeUnix(), 0) }

// Seal is the seal the capability carries, which a holder reads to know which
// installation and execution it is bound to.
func (d *Decoded) Seal() *basev0.WorkSealV1 { return d.context.GetSeal() }

// Decode reports whether a token is STRUCTURALLY a sealed capability of this
// package, and returns its claims as a Decoded when it is.
//
// # It returns the claims, and that was a correction
//
// The first version of this returned an error only, reasoning that returning
// claims would make it a third STRENGTH — a caller reads them, acts on them,
// and has authorized on an unverified token with no diff anywhere to show it.
// A client consumer pointed out what that argument missed: a holder reading
// its OWN credential needs the expiry and the seal, so with no claims returned
// it keeps its hand-written parser, and the second implementation this call
// exists to delete survives. Half the duplication removed is the half that
// matters least.
//
// The safety now rests where it does everywhere else in this package: on the
// TYPE, not on withholding data. Decoded is not Verified, cannot become it,
// and cannot become a Principal. The claims themselves were never secret —
// anyone holding the token can base64-decode them — so withholding them bought
// discipline from the honest caller and nothing from the careless one.
//
// # What it checks
//
// The shape, the encoding (ErrNotACoreToken for a foreign one), that the
// payload unmarshals and satisfies the schema — which is what requires a seal
// naming an installation and an epoch on every actor hop — and that the chain
// attenuates and a grant hop is well formed. Core's own sentinels, from core's
// own code: Verify and Inspect run the same decodeClaims and checkStructure.
//
// # What it does NOT check, and why there is no way to misread that
//
// No signature. No issuer. No audience. No window. No issuer state of any
// kind: not the authorization revision, not the live seal, not an epoch, not a
// binding, not a grant record. **A nil return means only that the token is
// shaped like a sealed capability. It authenticates nothing, and a caller that
// treats nil as permission has skipped verification entirely.** A hop that
// forwards on this has forwarded blind; a hop inspects (Inspector.Inspect).
//
// # What it is for
//
// A HOLDER reading its own credential, and a SENDER about to attach a
// capability to a request, asking "is this thing even sealed", so it refuses
// before the call rather than having the receiver refuse after. That is a
// different question from every verification entrypoint — Verify answers it
// for a receiver with the issuer's state, Authenticator for one with
// caller-supplied state, Inspector for a forwarding hop with a trust root and
// a route — and it exists here because consumers were answering it by hand
// and reaching different sentinels than core's fixtures declare.
func Decode(encoded string) (*Decoded, error) {
	wc, _, _, err := decodeClaims(encoded)
	if err != nil {
		return nil, err
	}
	if err := checkStructure(wc); err != nil {
		return nil, err
	}
	return &Decoded{context: wc}, nil
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
	// The SAME inputs Verify requires, because a verifier that could not have
	// verified this capability must not report on it either.
	if v.Revisions == nil || v.Grants == nil || v.Seals == nil || len(v.Keys) == 0 {
		return fmt.Errorf("work context: verifier is missing a revision source, grant source, seal source or key set")
	}
	wc := verified.claims()
	// THE CAPABILITY MUST BE THIS VERIFIER'S TO RE-CHECK.
	//
	// The safety argument for Recheck was that the argument cannot be obtained
	// except by verifying, so it cannot be a first verification. True, and
	// incomplete: a *Verified obtained from verifier X was accepted by
	// verifier Y, which never checked that the capability was addressed to it
	// at all. A gateway for one audience could re-check, and so report live,
	// a capability minted by another issuer for somebody else.
	//
	// Verify checks these before anything else; Recheck skipped them because
	// they do not CHANGE under a long-running call. That was the wrong test:
	// the question is not what can change, it is what this verifier is
	// entitled to answer about.
	// COMPARED UNCONDITIONALLY, exactly as Verify compares them.
	//
	// These were guarded with `v.Issuer != ""`, so a verifier with no issuer
	// and no audience — one Verify refuses outright — rechecked anything. That
	// guard is the fifth appearance of one shape in this package: an empty
	// signer policy meaning "a renderer", an empty digest meaning "bears no
	// execution", a zero applied record meaning "first generation", no build
	// record meaning the same, and an empty issuer meaning "do not check".
	// Each time the absent value was read as permission to skip.
	if wc.GetIssuer() != v.Issuer {
		return fmt.Errorf("%w: issued by %q and this verifier answers for %q", ErrInvalid, wc.GetIssuer(), v.Issuer)
	}
	if wc.GetAudience() != v.Audience {
		return fmt.Errorf("%w: addressed to %q and this verifier answers for %q", ErrInvalid, wc.GetAudience(), v.Audience)
	}
	// THE SIGNATURE, RE-VERIFIED under the verifier's CURRENT key — which is
	// the check, not a comparison standing in for it.
	//
	// Two attempts preceded this and both were weaker than the thing they
	// approximated. The first required a non-empty key map, which establishes
	// nothing. The second recorded the authenticating key on Verified and
	// compared it — but ed25519.PublicKey is a []byte, so what was recorded
	// was a REFERENCE into the verifier's own map: rotating a key in place
	// changed the "snapshot" too, and the comparison compared an array
	// against itself. Executed: after `copy(v.Keys[kid], other)`, Verify
	// refuses with "signature does not verify" and the comparison passed.
	//
	// Re-verifying removes the approximation and the duplicate rule with it.
	// Both reviews asked for this twice: one implementation of "is this
	// signature good under what we trust now", shared with Verify through
	// decodeClaims and ed25519.Verify, rather than a second rule in another
	// function that can drift from it. A rotated-out kid, a kid rotated in
	// place, and a capability signed by something else all reach the same
	// refusal here as they do there.
	_, claims, signature, err := decodeClaims(verified.Encoded())
	if err != nil {
		return err
	}
	// THE SAME authentication and THE SAME window Verify applies, by calling
	// the same functions rather than restating them. Both of Recheck's own
	// copies were missing a check: the key length (a panic) and not-before.
	if err := v.authenticate(wc, claims, signature); err != nil {
		return err
	}
	// ONE window check, and this is now true rather than half true.
	//
	// Recheck called checkWindow AND kept its own copy of the same two
	// comparisons, so "one checkWindow called by both" was a claim with two
	// bodies behind it — and the not-before test only discriminated when both
	// copies were deleted. The copy is gone; the shared check returns the
	// clock it used, which checkGrant below needs.
	now, err := v.checkWindow(wc)
	if err != nil {
		return err
	}
	current, err := v.Revisions.AuthorizationRevision(ctx, wc.GetTenantId())
	if err != nil {
		return fmt.Errorf("work context: authorization revision for tenant %q: %w", wc.GetTenantId(), err)
	}
	if wc.GetAuthorizationRevision() != current {
		return fmt.Errorf("%w: minted at revision %d, issuer is at %d", ErrRevoked, wc.GetAuthorizationRevision(), current)
	}
	if err := checkSealAgainst(ctx, v.Seals, wc); err != nil {
		return err
	}
	if hop := wc.GetGrantHop(); hop != nil {
		return v.checkGrant(ctx, wc, hop, now)
	}
	return nil
}
