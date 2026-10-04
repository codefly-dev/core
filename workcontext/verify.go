package workcontext

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"fmt"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// Verifier turns a presented token back into claims for one audience. Every
// field is load-bearing: a Verifier missing a replay store cannot enforce
// single-use, and one missing a grant source cannot check that an approval the
// capability claims is an approval the issuer recorded — so both are required
// rather than quietly skipped.
type Verifier struct {
	// Issuer is the authority this verifier trusts. A capability from any
	// other issuer is rejected even when its signature checks out against a
	// key this verifier holds.
	Issuer string

	// Audience is this service or trust boundary. A capability minted for
	// another audience is not a credential here.
	Audience string

	// Keys are the issuer's public keys by key id.
	Keys map[string]ed25519.PublicKey

	// Revisions answers the issuer's current authorization revision, which
	// is how revocation reaches a capability already in flight.
	Revisions RevisionSource

	// Replay consumes single-use capabilities.
	Replay ReplayStore

	// Grants resolves the approval a grant capability claims.
	Grants GrantSource

	// Seals answers the live installation, principal-epoch, build and
	// operation-binding state a capability's seal is compared against.
	// Required, like the others: a verifier without one could not tell a
	// capability sealed to a superseded installation from a current one, and
	// treating its absence as "sealing off" would make the strongest check in
	// the model the easiest one to omit.
	Seals SealSource

	// Now is the clock, for tests. nil means time.Now.
	Now func() time.Time

	// Skew is the tolerance applied to the capability's window. Zero means
	// DefaultSkew.
	Skew time.Duration

	// TrustTheConformanceFixtureKey allows this verifier to accept tokens
	// signed by the CONFORMANCE FIXTURE KEY, whose private half is derivable
	// from this package's source by anyone. Only the conformance kit sets it.
	//
	// It exists because the kit's key is reachable from production. Any binary
	// importing workcontext links FixtureKeyPair, and conformance.Verifier()
	// is an exported, ready-made verifier that trusts that key — so one
	// mistaken call, or one JWKS document that picked the fixture key up,
	// would make a real verifier accept tokens anybody can mint. A verifier
	// refuses that key unless this field says otherwise, which turns a silent
	// acceptance into a line somebody had to write and a reviewer can grep
	// for.
	//
	// NEVER set this outside a test. There is no legitimate production use:
	// the key signs conformance tokens and nothing else, ever.
	TrustTheConformanceFixtureKey bool
}

func (v *Verifier) now() time.Time {
	if v.Now == nil {
		return time.Now()
	}
	return v.Now()
}

func (v *Verifier) skew() time.Duration {
	if v.Skew == 0 {
		return DefaultSkew
	}
	return v.Skew
}

// Verify checks a presented capability and consumes it when it is single-use.
// Replay consumption happens last, so a capability rejected for any other
// reason is not burned by the attempt.
// resolveKey is THE key resolution, shared by Verify and Recheck: the key id
// must be one this verifier holds, the key must be well formed, and it must
// not be the conformance fixture key unless the caller said otherwise.
//
// It is one function because it was two. Recheck grew its own lookup when it
// started re-verifying signatures, and that copy omitted the length check —
// so a malformed key (a short hex decode, a half-finished rotation) PANICKED
// inside ed25519.Verify instead of refusing. The key is chosen by the
// untrusted capability's key id, so that was a crash of the process on
// demand for anyone who learned the id.
//
// Which is the same lesson this package keeps relearning: a second
// implementation of a check is where the next gap appears.
// authenticate is THE authentication: the key id must be one this verifier
// holds, the key must be well formed, it must not be the conformance fixture
// key unless the caller said so, and the signature must verify under it.
//
// It is one function because it was two, and the second one was missing a
// check. Recheck grew its own lookup and ed25519.Verify call while claiming
// in its own comment to be "one implementation shared with Verify" — and that
// copy dropped the length guard, so a nil, 16-byte or 33-byte key PANICKED
// with "ed25519: bad public key length" where Verify refused it. The key is
// chosen by the untrusted capability's key id, so that was a process crash on
// demand for anyone who learned a misconfigured id.
//
// The lesson is not "add the guard back". It is that a comment claiming one
// implementation is worthless while there are two bodies: this package has now
// grown a second copy of a check three times, and each time the copy was the
// one with the hole.
func (v *Verifier) authenticate(wc *basev0.WorkContextV1, claims, signature []byte) error {
	key, err := v.resolveKey(wc.GetKeyId())
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, claims, signature) {
		return fmt.Errorf("%w: signature does not verify under key %q as this verifier holds it now",
			ErrInvalid, wc.GetKeyId())
	}
	return nil
}

// checkWindow is THE window check, both ends, called by Verify and by
// Recheck. Recheck had its own and it omitted not-before.
func (v *Verifier) checkWindow(wc *basev0.WorkContextV1) error {
	now, skew := v.now(), v.skew()
	if notBefore := time.Unix(wc.GetNotBeforeUnix(), 0); now.Add(skew).Before(notBefore) {
		return fmt.Errorf("%w: not valid before %s", ErrInvalid, notBefore.UTC().Format(time.RFC3339))
	}
	if expires := time.Unix(wc.GetExpiresAtUnix(), 0); !now.Add(-skew).Before(expires) {
		return fmt.Errorf("%w: expired at %s", ErrInvalid, expires.UTC().Format(time.RFC3339))
	}
	return nil
}

func (v *Verifier) resolveKey(keyID string) (ed25519.PublicKey, error) {
	key, known := v.Keys[keyID]
	if !known {
		return nil, fmt.Errorf("%w: no verification key %q; this verifier does not hold, or no longer holds, the key named by this capability",
			ErrInvalid, keyID)
	}
	// ed25519.Verify PANICS on a key that is not PublicKeySize bytes. A
	// malformed key verifies nothing, which is a refusal like any other.
	if len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: verification key %q is %d bytes, not %d",
			ErrInvalid, keyID, len(key), ed25519.PublicKeySize)
	}
	if !v.TrustTheConformanceFixtureKey && isFixtureKey(key) {
		return nil, fmt.Errorf("%w: key %q is the conformance fixture key, which anyone can derive from core's source; set TrustTheConformanceFixtureKey only in a conformance run",
			ErrInvalid, keyID)
	}
	return key, nil
}

func (v *Verifier) Verify(ctx context.Context, encoded string) (*Verified, error) {
	if v.Revisions == nil || v.Replay == nil || v.Grants == nil || v.Seals == nil {
		return nil, fmt.Errorf("work context: verifier is missing a revision source, replay store, grant source or seal source")
	}
	wc, claims, sig, err := decodeClaims(encoded)
	if err != nil {
		return nil, err
	}
	if wc.GetIssuer() != v.Issuer {
		return nil, fmt.Errorf("%w: issued by %q, not %q", ErrInvalid, wc.GetIssuer(), v.Issuer)
	}
	if err := v.authenticate(wc, claims, sig); err != nil {
		return nil, err
	}
	if wc.GetAudience() != v.Audience {
		return nil, fmt.Errorf("%w: minted for audience %q, presented to %q", ErrInvalid, wc.GetAudience(), v.Audience)
	}
	if err := v.checkWindow(wc); err != nil {
		return nil, err
	}
	// Kept for the grant window and the replay entry's expiry below, which
	// need the same clock the window check used.
	now, skew := v.now(), v.skew()
	expires := time.Unix(wc.GetExpiresAtUnix(), 0)
	if err := checkStructure(wc); err != nil {
		return nil, err
	}

	current, err := v.Revisions.AuthorizationRevision(ctx, wc.GetTenantId())
	if err != nil {
		return nil, fmt.Errorf("work context: authorization revision for tenant %q: %w", wc.GetTenantId(), err)
	}
	// Exact equality, not "at least". A capability carrying a revision HIGHER
	// than the issuer's live one is refused too, for the reason the seal's own
	// comment already gave and this check did not follow: there is no
	// legitimate way to hold one. The shapes that produce it are a rolled-back
	// issuer and a forged claim, and neither is a thing to accept. The
	// argument was in the package and applied to one lever but not the other.
	if wc.GetAuthorizationRevision() != current {
		return nil, fmt.Errorf("%w: minted at revision %d, issuer is at %d", ErrRevoked, wc.GetAuthorizationRevision(), current)
	}
	// The seal is checked before the grant hop and before replay consumption:
	// a capability sealed to a superseded installation is not a credential, and
	// burning its nonce on the way to refusing it would turn a re-mintable
	// refusal into a permanent one.
	if err := checkSealAgainst(ctx, v.Seals, wc); err != nil {
		return nil, err
	}
	if hop := wc.GetGrantHop(); hop != nil {
		if err := v.checkGrant(ctx, wc, hop, now); err != nil {
			return nil, err
		}
	}
	if wc.GetReplayPolicy() == ReplaySingleUse {
		if err := v.Replay.Consume(ctx, wc.GetNonce(), expires.Add(skew)); err != nil {
			return nil, err
		}
	}

	return &Verified{context: wc, encoded: encoded, sha256: Fingerprint(encoded)}, nil
}

// CheckEncoding reports whether a payload is in another format entirely,
// returning ErrNotACoreToken when it is and nil when it is not.
//
// It takes the base64url-DECODED payload bytes — the claims, not the token.
// Verify calls it before unmarshalling and well before the signature check,
// and it is exported so that every other place in the fleet that decodes a
// payload can name the same condition with the same error. That matters more
// than it looks: a second decoder that answered "payload is not a
// WorkContextV1" for a foreign encoding would give an operator two different
// messages for one condition, which is the diagnostic fragmentation this whole
// error exists to end — in miniature.
//
// A nil return means ONLY that the payload is not visibly in another format.
// It does not mean the payload is a valid capability, and it does not mean
// anything was authenticated: nothing here checks a signature, and a caller
// that treats nil as permission has skipped verification entirely. Verify is
// the only thing that turns a token into claims.
//
// A JSON payload is detectable, and the reasoning has to be stated carefully
// because the version here was WRONG about why.
//
// It said: "{" is 0x7b, field 15 with wire type 3 — the start-group type,
// which proto3 does not emit AND WHICH UNMARSHAL REFUSES. That last clause is
// false, executed: proto.Unmarshal([]byte{0x7b, 0x7c}, &WorkContextV1{})
// returns nil. A well-formed group is parsed and dropped as an unknown field.
//
// What is true, and is all this check needs: core's minter emits a
// deterministic proto3 encoding whose first field is field 1 with wire type 2,
// so the first byte is 0x0a and never 0x7b or 0x5b. A payload beginning with
// either was produced by something else, which is what ErrNotACoreToken says.
// The conclusion stood; the premise did not, and a premise that does not hold
// is how a check gets removed later by someone who tests it.
//
// Leading whitespace is skipped before the test because a JSON encoder may emit
// it, and a payload that is whitespace followed by "{" is no more a core token
// than one that starts with it.
func CheckEncoding(claims []byte) error {
	// An empty payload is NOT this error. "Not a core token" means "this is
	// another format", and an empty payload is not another format — it is a
	// malformed token of no format at all, which the schema check below refuses
	// as invalid. Widening this error to cover it would make it mean "something
	// was wrong early", which is exactly the vagueness it exists to remove.
	// A UTF-8 BOM first: an encoder that emits one before JSON produces a
	// payload that is no more a core token than one starting with "{", and
	// 0xEF is not a tag proto3 emits either. Trimming it is what lets the
	// discrimination NAME the format rather than leaving it to be refused
	// later as merely invalid.
	trimmed := bytes.TrimPrefix(claims, []byte{0xEF, 0xBB, 0xBF})
	trimmed = bytes.TrimLeft(trimmed, " \t\r\n")
	if len(trimmed) == 0 {
		return nil
	}
	switch trimmed[0] {
	case '{':
		return fmt.Errorf("%w: the payload is a JSON object; this capability is a deterministic protobuf encoding", ErrNotACoreToken)
	case '[':
		return fmt.Errorf("%w: the payload is a JSON array; this capability is a deterministic protobuf encoding", ErrNotACoreToken)
	}
	return nil
}

// checkGrant holds the capability's grant hop against the issuer's own record
// of the approval. Without this a signed capability could claim any approval,
// and revoking a grant would stop nothing.
func (v *Verifier) checkGrant(ctx context.Context, wc *basev0.WorkContextV1, hop *basev0.WorkGrantHopV1, now time.Time) error {
	grant, err := v.Grants.Grant(ctx, hop.GetGrantId())
	if err != nil {
		return fmt.Errorf("work context: grant %q: %w", hop.GetGrantId(), err)
	}
	if err := grant.validate(); err != nil {
		return err
	}
	if grant.Revoked {
		return fmt.Errorf("%w: grant %q is revoked", ErrRevoked, grant.ID)
	}
	if !now.Before(grant.NotAfter) {
		return fmt.Errorf("%w: grant %q closed at %s", ErrInvalid, grant.ID, grant.NotAfter.UTC().Format(time.RFC3339))
	}
	if grant.Audience != wc.GetAudience() {
		return fmt.Errorf("%w: grant %q was approved for %q, capability names %q", ErrInvalid, grant.ID, grant.Audience, wc.GetAudience())
	}
	if grant.Subject != hop.GetSubject() {
		return fmt.Errorf("%w: grant %q is pinned to subject %q, capability claims %q", ErrInvalid, grant.ID, grant.Subject, hop.GetSubject())
	}
	if grant.RequestDigest != hop.GetRequestDigest() {
		return fmt.Errorf("%w: grant %q is pinned to another request", ErrInvalid, grant.ID)
	}
	if !proto.Equal(grant.Scope, hop.GetGrantedScope()) {
		return fmt.Errorf("%w: grant %q approved another scope", ErrInvalid, grant.ID)
	}
	if !slices.Equal(approverKeys(grant.Approvers), claimedApproverKeys(hop.GetApprovers())) {
		return fmt.Errorf("%w: grant %q names other approvers", ErrInvalid, grant.ID)
	}
	if grant.AuthorizationRevision != wc.GetAuthorizationRevision() {
		return fmt.Errorf("%w: grant %q was decided at revision %d, capability claims %d", ErrInvalid, grant.ID, grant.AuthorizationRevision, wc.GetAuthorizationRevision())
	}
	return nil
}

// checkStructure enforces the properties the schema cannot state: the chain
// attenuates, and a grant hop is the audited exception rather than a way to
// skip the rule.
func checkStructure(wc *basev0.WorkContextV1) error {
	grant := wc.GetGrantHop()
	chain := wc.GetActorChain()
	effective := wc.GetAuthorityScopes()
	for index, hop := range chain {
		elevated := grant != nil && index == len(chain)-1
		if !elevated && !ScopesAttenuate(hop.GetGrantedScopes(), effective) {
			return fmt.Errorf("%w: actor hop %d (%s) widens authority beyond the hop before it", ErrInvalid, index, hop.GetPrincipalId())
		}
		effective = hop.GetGrantedScopes()
	}
	if grant == nil {
		return nil
	}
	if len(chain) == 0 {
		return fmt.Errorf("%w: grant %q has no actor hop to elevate", ErrInvalid, grant.GetGrantId())
	}
	if wc.ParentSessionId == nil {
		return fmt.Errorf("%w: grant %q is not a child session", ErrInvalid, grant.GetGrantId())
	}
	if wc.GetReplayPolicy() != ReplaySingleUse {
		return fmt.Errorf("%w: grant %q is replayable; a grant authorizes one call", ErrInvalid, grant.GetGrantId())
	}
	scope := grant.GetGrantedScope()
	if len(scope.GetActions()) != 1 || len(scope.GetResourceIds()) != 1 {
		return fmt.Errorf("%w: grant %q must name exactly one action on one resource", ErrInvalid, grant.GetGrantId())
	}
	last := chain[len(chain)-1]
	if len(last.GetGrantedScopes()) != 1 || !proto.Equal(last.GetGrantedScopes()[0], scope) {
		return fmt.Errorf("%w: grant %q elevates a hop holding something other than the granted scope", ErrInvalid, grant.GetGrantId())
	}
	if last.GetDelegationId() != grant.GetGrantId() {
		return fmt.Errorf("%w: grant %q elevates a hop delegated by %q", ErrInvalid, grant.GetGrantId(), last.GetDelegationId())
	}
	elevated := identityOf(wc, last)
	actor := ownerIdentity(wc)
	if len(chain) > 1 {
		actor = identityOf(wc, chain[len(chain)-2])
	}
	if elevated != actor {
		return fmt.Errorf("%w: grant %q hands authority to %v rather than elevating %v", ErrInvalid, grant.GetGrantId(), elevated, actor)
	}
	return nil
}

// actorIdentity is everything a derived Principal reads off a hop. The grant
// hop must match the hop it elevates on all of it: keeping the id while
// changing the kind or the agent identity would produce a different
// Principal holding the approved scope.
type actorIdentity struct {
	principalID  string
	kind         string
	agentID      string
	organization string
}

// identityOf resolves a hop's identity, including the organization it
// inherits from the task when it does not name its own.
func identityOf(wc *basev0.WorkContextV1, hop *basev0.WorkActorV1) actorIdentity {
	organization := hop.GetOrganizationId()
	if organization == "" {
		organization = wc.GetOrganizationId()
	}
	return actorIdentity{
		principalID:  hop.GetPrincipalId(),
		kind:         hop.GetPrincipalKind(),
		agentID:      hop.GetAgentId(),
		organization: organization,
	}
}

func ownerIdentity(wc *basev0.WorkContextV1) actorIdentity {
	return actorIdentity{
		principalID:  wc.GetOwnerPrincipalId(),
		kind:         wc.GetOwnerPrincipalKind(),
		agentID:      wc.GetOwnerAgentId(),
		organization: wc.GetOrganizationId(),
	}
}

func approverKeys(approvers []Approver) []string {
	keys := make([]string, 0, len(approvers))
	for _, approver := range approvers {
		keys = append(keys, approver.PrincipalID+"\x00"+approver.Kind)
	}
	slices.Sort(keys)
	return keys
}

func claimedApproverKeys(approvers []*basev0.WorkApproverV1) []string {
	keys := make([]string, 0, len(approvers))
	for _, approver := range approvers {
		keys = append(keys, approver.GetPrincipalId()+"\x00"+approver.GetPrincipalKind())
	}
	slices.Sort(keys)
	return keys
}
