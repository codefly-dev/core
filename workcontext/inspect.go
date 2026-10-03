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
	// The bound belongs in the ONE decode path, not in one entrypoint. It was
	// enforced only in Inspect, so a caller that reached Verify directly — the
	// strong path, and the one a receiver actually uses — had no bound at all.
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

// Inspected is a token that is structurally a sealed capability of this
// package. **Nothing about it has been authenticated.**
//
// It is a distinct type from Verified and Authenticated for the same reason
// those are distinct from each other: the type carries which question was
// answered. Verified means the issuer's state agreed. Authenticated means the
// caller's supplied state agreed. Inspected means only that the bytes are
// shaped like a sealed capability — no signature was checked and no state was
// consulted, so a forgery produces one exactly as readily as a real
// capability does, and a test asserts that.
//
// What it is therefore FOR: reading a credential you already obtained some
// other way — your own, handed to you by your issuer over an authenticated
// channel — so you can see its expiry and its seal without re-deriving them
// by hand. Reading your own credential is not an authorization decision.
//
// What it cannot do: become authority. policy.PrincipalFromWorkContext takes a
// *Verified, and Authority.Child and Authority.Grant take one too, so an
// Inspected capability cannot become a Principal or be exchanged for a derived
// capability. There is no conversion in either direction and a test guards
// against one being added.
type Inspected struct {
	context *basev0.WorkContextV1
}

// Context is the claims as they were carried. They are DATA, not findings: no
// signature stands behind them.
func (i *Inspected) Context() *basev0.WorkContextV1 { return i.context }

// ExpiresAt is when the capability's window closes — the field a holder reads
// to know when to mint afresh.
func (i *Inspected) ExpiresAt() time.Time { return time.Unix(i.context.GetExpiresAtUnix(), 0) }

// NotBefore is when the window opens.
func (i *Inspected) NotBefore() time.Time { return time.Unix(i.context.GetNotBeforeUnix(), 0) }

// Seal is the seal the capability carries, which a holder reads to know which
// installation and execution it is bound to.
func (i *Inspected) Seal() *basev0.WorkSealV1 { return i.context.GetSeal() }

// Inspect reports whether a token is STRUCTURALLY a sealed capability of this
// package, and returns its claims as an Inspected when it is.
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
// TYPE, not on withholding data. Inspected is not Verified, cannot become it,
// and cannot become a Principal. The claims themselves were never secret —
// anyone holding the token can base64-decode it — so withholding them bought
// discipline from the honest caller and nothing from the careless one.
//
// # What it checks
//
// The shape, the encoding (ErrNotACoreToken for a foreign one), that the
// payload unmarshals and satisfies the schema — which is what now requires a
// seal naming an installation and an epoch on every actor hop — and that the
// chain attenuates and a grant hop is well formed. Core's own sentinels, from
// core's own code: Verify runs the same decodeClaims and checkStructure.
//
// # What it does NOT check, and why there is no way to misread that
//
// No signature. No issuer. No audience. No window. No issuer state of any
// kind: not the authorization revision, not the live seal, not an epoch, not a
// binding, not a grant record. **A nil return means only that the token is
// shaped like a sealed capability. It authenticates nothing, and a caller that
// treats nil as permission has skipped verification entirely.**
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
func Inspect(encoded string) (*Inspected, error) {
	wc, _, _, err := decodeClaims(encoded)
	if err != nil {
		return nil, err
	}
	if err := checkStructure(wc); err != nil {
		return nil, err
	}
	return &Inspected{context: wc}, nil
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
	wc := verified.claims()
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
