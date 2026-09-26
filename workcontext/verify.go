package workcontext

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"time"

	"buf.build/go/protovalidate"
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

	// Now is the clock, for tests. nil means time.Now.
	Now func() time.Time

	// Skew is the tolerance applied to the capability's window. Zero means
	// DefaultSkew.
	Skew time.Duration
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
func (v *Verifier) Verify(ctx context.Context, encoded string) (*Verified, error) {
	if v.Revisions == nil || v.Replay == nil || v.Grants == nil {
		return nil, fmt.Errorf("work context: verifier is missing a revision source, replay store or grant source")
	}
	payload, signature, found := strings.Cut(encoded, ".")
	if !found {
		return nil, fmt.Errorf("%w: token is not <payload>.<signature>", ErrInvalid)
	}
	claims, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: payload is not base64url: %v", ErrInvalid, err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return nil, fmt.Errorf("%w: signature is not base64url: %v", ErrInvalid, err)
	}
	wc := &basev0.WorkContextV1{}
	if err := proto.Unmarshal(claims, wc); err != nil {
		return nil, fmt.Errorf("%w: payload is not a WorkContextV1: %v", ErrInvalid, err)
	}
	if err := protovalidate.Validate(wc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if wc.GetIssuer() != v.Issuer {
		return nil, fmt.Errorf("%w: issued by %q, not %q", ErrInvalid, wc.GetIssuer(), v.Issuer)
	}
	key, known := v.Keys[wc.GetKeyId()]
	if !known {
		return nil, fmt.Errorf("%w: no verification key %q", ErrInvalid, wc.GetKeyId())
	}
	// ed25519.Verify panics on a key that is not PublicKeySize bytes, and the
	// key is chosen by the untrusted capability's key id — so a single
	// misconfigured entry (a short hex decode, a half-finished rotation) would
	// turn every capability naming it into a crash of this process rather than a
	// refusal, on demand for anyone who learns that key id. A malformed key
	// verifies nothing, which is a refusal like any other.
	if len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: verification key %q is %d bytes, not %d", ErrInvalid, wc.GetKeyId(), len(key), ed25519.PublicKeySize)
	}
	if !ed25519.Verify(key, claims, sig) {
		return nil, fmt.Errorf("%w: signature does not verify under key %q", ErrInvalid, wc.GetKeyId())
	}
	if wc.GetAudience() != v.Audience {
		return nil, fmt.Errorf("%w: minted for audience %q, presented to %q", ErrInvalid, wc.GetAudience(), v.Audience)
	}

	now := v.now()
	skew := v.skew()
	if notBefore := time.Unix(wc.GetNotBeforeUnix(), 0); now.Add(skew).Before(notBefore) {
		return nil, fmt.Errorf("%w: not valid before %s", ErrInvalid, notBefore.UTC().Format(time.RFC3339))
	}
	expires := time.Unix(wc.GetExpiresAtUnix(), 0)
	if !now.Add(-skew).Before(expires) {
		return nil, fmt.Errorf("%w: expired at %s", ErrInvalid, expires.UTC().Format(time.RFC3339))
	}
	if err := checkStructure(wc); err != nil {
		return nil, err
	}

	current, err := v.Revisions.AuthorizationRevision(ctx, wc.GetTenantId())
	if err != nil {
		return nil, fmt.Errorf("work context: authorization revision for tenant %q: %w", wc.GetTenantId(), err)
	}
	if wc.GetAuthorizationRevision() < current {
		return nil, fmt.Errorf("%w: minted at revision %d, issuer is at %d", ErrRevoked, wc.GetAuthorizationRevision(), current)
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
