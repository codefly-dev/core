package workcontext

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"time"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// Authority mints capabilities. It holds the issuer's signing key, so exactly
// one component in a deployment constructs one.
type Authority struct {
	// Issuer names the authority that verified identity and minted the
	// capability. Verifiers pin it.
	Issuer string

	// KeyID selects the public key a verifier checks the signature with,
	// so the pair can rotate without a flag day.
	KeyID string

	// Key signs. Ed25519 is the only algorithm a v1 capability declares.
	Key ed25519.PrivateKey

	// Revisions answers the issuer's current authorization revision. Minting
	// against a stale one produces a capability every verifier rejects, so
	// the minter reads the revision rather than inheriting it.
	Revisions RevisionSource

	// MaxTTL is the longest window this authority will mint. Zero means
	// DefaultMaxTTL; see that constant for why there is a ceiling at all.
	MaxTTL time.Duration

	// Seals answers the live installation, principal-epoch, build and
	// operation-binding state every capability is sealed to. The minter reads
	// them for the same reason it reads the revision: a seal taken as input is
	// a seal the caller could invent, and the capability would fail in another
	// process as an authentication error rather than here as a refusal.
	Seals SealSource

	// Now is the clock, for tests. nil means time.Now.
	Now func() time.Time
}

func (a *Authority) now() time.Time {
	if a.Now == nil {
		return time.Now()
	}
	return a.Now()
}

// StartInput describes the first session of a task.
type StartInput struct {
	TenantID           string                // required; first element of the task identity tuple
	OwnerPrincipalID   string                // required; whose authority the task runs on
	OwnerPrincipalKind string                // required; human, service or agent
	OwnerAgentID       string                // required when OwnerPrincipalKind is agent
	TaskID             string                // required; survives every child session
	Audience           string                // required; the service allowed to consume this capability
	AuthorityScopes    []*basev0.WorkScopeV1 // the maximum authority the owner delegated in
	AttributionTeamIDs []string              // optional; verified attribution snapshot
	WorkspaceID        string                // optional
	ProjectID          string                // optional; narrows WorkspaceID
	OrganizationID     string                // the organization the task runs in
	ReplayPolicy       string                // "" defaults to idempotent
	TTL                time.Duration         // required; must be positive

	// InstallationID is the installation this capability is sealed to.
	// Required: authority is held through an installation, so a capability
	// naming none is bound to nothing. The revision, the principal's epoch and
	// the build incarnation are read from the Authority's seal source, not
	// taken from here.
	InstallationID string

	// OperationBindingID is the unit of authority this capability exercises,
	// when it exercises one. Empty for a session that does not. Its revision
	// and incarnation are read from the seal source.
	OperationBindingID string

	// Execution is what the caller attests it is running: the image-manifest
	// digest and the incarnation. REQUIRED, and checked against the approved
	// build the issuer holds rather than recorded.
	//
	// It is an input because only the host can resolve it — from the
	// authenticated workload's own status, never from something the process
	// asserts over the wire — and it is checked because an unchecked input is
	// a claim. See workcontext.Execution.
	Execution Execution
}

// Start mints the first capability of a task, carrying the owner's delegated
// authority and no actor hop: the owner acting directly is not dressed up as
// a delegation to itself.
func (a *Authority) Start(ctx context.Context, in StartInput) (string, *basev0.WorkContextV1, error) {
	if err := a.checkTTL("start", in.TTL); err != nil {
		return "", nil, err
	}
	if in.OwnerPrincipalKind == "" {
		return "", nil, fmt.Errorf("%w: start needs the owner's principal kind", ErrInvalid)
	}
	revision, err := a.revision(ctx, in.TenantID)
	if err != nil {
		return "", nil, err
	}
	seal, binding, err := a.sealFor(ctx, in.OwnerPrincipalID, in.InstallationID, in.OperationBindingID, in.OwnerPrincipalID, in.Execution)
	if err != nil {
		return "", nil, err
	}
	replay := in.ReplayPolicy
	if replay == "" {
		replay = ReplayIdempotent
	}
	now := a.now()
	wc := &basev0.WorkContextV1{
		Typ:                   Typ,
		Algorithm:             Algorithm,
		KeyId:                 a.KeyID,
		Issuer:                a.Issuer,
		Audience:              in.Audience,
		NotBeforeUnix:         now.Unix(),
		IssuedAtUnix:          now.Unix(),
		ExpiresAtUnix:         now.Add(in.TTL).Unix(),
		Nonce:                 uuid.NewString(),
		AuthorizationRevision: revision,
		ReplayPolicy:          replay,
		TenantId:              in.TenantID,
		OwnerPrincipalId:      in.OwnerPrincipalID,
		TaskId:                in.TaskID,
		SessionId:             uuid.NewString(),
		AuthorityScopes:       cloneScopes(in.AuthorityScopes),
		AttributionTeamIds:    slices.Clone(in.AttributionTeamIDs),
		Seal:                  seal,
		OperationBinding:      binding,
	}
	setOptional(&wc.OwnerPrincipalKind, in.OwnerPrincipalKind)
	setOptional(&wc.OwnerAgentId, in.OwnerAgentID)
	setOptional(&wc.OrganizationId, in.OrganizationID)
	setOptional(&wc.WorkspaceId, in.WorkspaceID)
	setOptional(&wc.ProjectId, in.ProjectID)
	return a.seal(wc)
}

// ChildInput describes one delegation hop.
type ChildInput struct {
	PrincipalID    string                // required; the actor this hop delegates to
	PrincipalKind  string                // required; human, service or agent
	AgentID        string                // required when PrincipalKind is agent
	DelegationID   string                // required; the delegation that produced the hop
	OrganizationID string                // the actor's own organization, when it differs from the task's
	GrantedScopes  []*basev0.WorkScopeV1 // required; must attenuate the parent's effective scopes
	Audience       string                // required; the service allowed to consume this capability
	ReplayPolicy   string                // "" defaults to idempotent
	TTL            time.Duration         // required; clamped to the parent's expiry

	// Execution is what this hop attests it is running, and it is REQUIRED
	// when the hop's principal bears an approved execution — refused when it
	// bears none, which is a human taking over a session.
	//
	// It exists because minting was execution-bound only at Start: a
	// derivation inherited the parent's execution and attested nothing, so a
	// caller holding a parent capability derived children whatever it was
	// running. See Authority.sealExecutionFor.
	Execution Execution

	// OperationBindingID is the unit of authority this hop exercises, when it
	// exercises one. Empty carries the parent's; naming one replaces it, and
	// the replacement's revision and incarnation are read live.
	OperationBindingID string
}

// Child exchanges a verified parent capability for a delegated one. The hop's
// scopes must attenuate the parent's effective scopes and the child expires no
// later than the parent: an exchange narrows authority or leaves it alone,
// never the reverse.
func (a *Authority) Child(ctx context.Context, parent *Verified, in ChildInput) (string, *basev0.WorkContextV1, error) {
	if parent == nil {
		return "", nil, fmt.Errorf("%w: child needs a verified parent", ErrInvalid)
	}
	if parent.claims().GetGrantHop() != nil {
		return "", nil, fmt.Errorf("%w: a grant capability authorizes one call and delegates nothing", ErrInvalid)
	}
	if len(in.GrantedScopes) == 0 {
		return "", nil, fmt.Errorf("%w: child hop %q grants no scope", ErrInvalid, in.PrincipalID)
	}
	if !ScopesAttenuate(in.GrantedScopes, parent.EffectiveScopes()) {
		return "", nil, fmt.Errorf("%w: child hop %q widens authority beyond its parent", ErrInvalid, in.PrincipalID)
	}
	if err := a.checkTTL(fmt.Sprintf("child hop %q", in.PrincipalID), in.TTL); err != nil {
		return "", nil, err
	}
	replay := in.ReplayPolicy
	if replay == "" {
		replay = ReplayIdempotent
	}
	revision, err := a.carryForwardRevision(ctx, parent)
	if err != nil {
		return "", nil, err
	}
	hop := &basev0.WorkActorV1{
		PrincipalId:   in.PrincipalID,
		PrincipalKind: in.PrincipalKind,
		DelegationId:  in.DelegationID,
		GrantedScopes: cloneScopes(in.GrantedScopes),
	}
	setOptional(&hop.AgentId, in.AgentID)
	setOptional(&hop.OrganizationId, in.OrganizationID)
	wc, err := a.derive(parent, in.Audience, replay, a.now().Add(in.TTL), revision)
	if err != nil {
		return "", nil, err
	}
	wc.ActorChain = append(wc.ActorChain, hop)
	if err := a.deriveSeal(ctx, parent, wc, in.OperationBindingID, in.PrincipalID, in.Execution); err != nil {
		return "", nil, err
	}
	return a.seal(wc)
}

// deriveSeal prepares a derived capability's seal: it holds the INHERITED seal
// against live state and refuses the derivation if any of it has moved, stamps
// the new hop's own epoch, and resolves a replacement operation binding when
// the hop names one.
//
// Nothing is overwritten with current counters. See carryForwardSeal for why
// that is the whole security content of this function rather than a detail: a
// parent whose sealed state has moved is a revoked credential, and a
// derivation that restamped it would let revocation be defeated by deriving.
//
// The installation is always the parent's: a delegation hop narrows authority
// within one installation and never moves it, so taking an installation from
// the hop would be a way to widen across installations.
// requireTrustedParent re-verifies the parent's signature under THIS
// authority's current key for its id.
//
// A *Verified proves somebody verified those bytes; it does not prove the
// DERIVING issuer trusts the key they were verified under. So a parent
// authenticated under a key this authority has rotated out — or never held —
// was derived from happily, and the child was signed with this authority's own
// key, laundering an untrusted parent into a trusted child. The same shape as
// Recheck's: holding a *Verified is not the same question as "is this good
// under what we trust now".
func (a *Authority) requireTrustedParent(parent *Verified) error {
	if len(a.Key) != ed25519.PrivateKeySize {
		return fmt.Errorf("work context: authority has no signing key, so it can derive nothing")
	}
	claims := parent.claims()
	if claims.GetKeyId() != a.KeyID {
		return fmt.Errorf("%w: the parent was authenticated under key %q and this issuer signs with %q, so nothing may be derived from it",
			ErrInvalid, claims.GetKeyId(), a.KeyID)
	}
	_, payload, signature, err := decodeClaims(parent.Encoded())
	if err != nil {
		return err
	}
	// Under THIS issuer's own key, as it holds it now. A rotated key refuses
	// the derivation rather than producing a child signed with new material
	// from a parent the new material never signed.
	public, ok := a.Key.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("work context: this issuer's signing key is not an ed25519 key")
	}
	if !ed25519.Verify(public, payload, signature) {
		return fmt.Errorf("%w: the parent's signature does not verify under key %q as this issuer holds it now",
			ErrInvalid, a.KeyID)
	}
	return nil
}

func (a *Authority) deriveSeal(ctx context.Context, parent *Verified, wc *basev0.WorkContextV1, bindingID, exercising string, attested Execution) error {
	if err := a.requireTrustedParent(parent); err != nil {
		return err
	}
	if err := a.carryForwardSeal(ctx, parent.claims()); err != nil {
		return err
	}
	// THE HOP ATTESTS ITS OWN EXECUTION, and this is the blocker that closed.
	//
	// Minting was execution-bound only at Start. A derivation inherited the
	// parent's execution and attested nothing, so a caller holding a parent
	// capability derived children whatever it was running — which is the
	// threat the execution fields exist to stop, left open at every hop but
	// the first. The old comment here said a derivation "does not re-attest:
	// it is the same execution continuing", and that is true only when the
	// hop is the same workload. For a NEW principal it is an assumption with
	// nothing behind it.
	//
	// So the seal's execution now describes whoever EXERCISES the capability,
	// read from ApprovedBuild for that principal, and the caller attests it.
	// A hop whose principal bears no execution — a human taking over a
	// session — carries none and must attest none.
	if err := a.sealExecutionFor(ctx, wc, exercising, attested); err != nil {
		return err
	}
	// The hop's own epoch, read live. A hop is new authority for a new
	// principal, so its epoch is current by construction — unlike the seal,
	// which is inherited and must not move.
	epoch, err := a.epochFor(ctx, exercising)
	if err != nil {
		return err
	}
	hop := wc.ActorChain[len(wc.ActorChain)-1]
	hop.PrincipalEpoch = epoch

	if bindingID == "" {
		// Keep the parent's binding, which carryForwardSeal has already held
		// against live state. Re-resolving it would be the restamping this
		// function exists not to do.
		//
		// But keeping it is only right when the hop that will EXERCISE the
		// capability is the principal the binding was granted to. A binding is
		// held by a principal, not carried by a session: the verifier requires
		// that the resolved binding is granted to the exercising principal, so
		// a hop for a new principal that inherited the owner's binding minted
		// a capability EVERY Verify refuses — and refuses as ErrRevoked, whose
		// text says "authorization revision superseded", sending whoever
		// debugs it after a revocation that never happened.
		//
		// Silently dropping the binding instead would be worse than the
		// unverifiable token: with no binding the verifier's whole
		// binding check returns early, so the derivation would have WIDENED
		// authority by removing the unit it was bound to. So this refuses, and
		// names the input that resolves it.
		if held := wc.GetOperationBinding(); held != nil {
			binding, err := a.Seals.OperationBinding(ctx, held.GetBindingId())
			if err != nil {
				return fmt.Errorf("work context: operation binding %q: %w", held.GetBindingId(), err)
			}
			if binding.PrincipalID != exercising {
				return fmt.Errorf("%w: the parent exercises binding %q, which is granted to %q, and this hop is exercised by %q; name the binding this hop holds in OperationBindingID, because a binding is held by a principal and does not travel with a delegation",
					ErrInvalid, held.GetBindingId(), binding.PrincipalID, exercising)
			}
		}
		return nil
	}
	inherited := sealOf(wc)
	// THE HOP'S OWN EXECUTION, read back off the hop that sealExecutionFor
	// just stamped — not the seal's, which is the OWNER's.
	//
	// This passed the seal's execution while naming the HOP as exercising, so
	// replacing a binding re-attested the owner's execution as the child's.
	// It is invisible whenever both principals run the same build, which is
	// what the existing binding test did, so nothing caught it: the edge the
	// per-hop fix left behind.

	_, binding, err := a.sealFor(ctx, wc.GetOwnerPrincipalId(), inherited.GetInstallationId(), bindingID, exercising,
		Execution{ImageDigest: hop.GetImageDigest(), BuildIncarnation: hop.GetBuildIncarnation()})
	if err != nil {
		return err
	}
	wc.OperationBinding = binding
	return nil
}

// sealExecutionFor records, on a derived capability's inherited seal, the
// execution the issuer approves for the principal that will exercise it —
// having first required the caller to attest that same execution.
//
// It is not "restamping", which carryForwardSeal exists to prevent, and the
// distinction is worth being exact about. Restamping means overwriting
// INHERITED state with current counters, so a capability whose installation or
// epoch has moved is silently renewed by deriving; that remains refused.
// The execution is not inherited state — it belongs to whoever is running, and
// a new hop is a new principal who may be running something else entirely.
// Leaving the parent's execution in place was the thing that made a derived
// capability's execution a claim nobody checked.
func (a *Authority) sealExecutionFor(ctx context.Context, wc *basev0.WorkContextV1, exercising string, attested Execution) error {
	digest, incarnation, err := a.Seals.ApprovedBuild(ctx, exercising)
	bearsNone := errors.Is(err, ErrNoApprovedBuild)
	if err != nil && !bearsNone {
		return fmt.Errorf("work context: approved build for principal %q: %w", exercising, err)
	}
	if wc.GetSeal() == nil {
		return fmt.Errorf("%w: a derived capability inherits a seal, and this one carries none", ErrInvalid)
	}
	// THE HOP'S OWN EXECUTION GOES ON THE HOP, and the seal's — the OWNER's —
	// is never touched.
	//
	// This used to write the hop's execution onto the seal, which has one
	// slot. So a derivation overwrote the owner's execution with the last
	// hop's, and superseding the owner's build refused the owner's own
	// capability while every child of it verified and went on minting
	// grandchildren. The seal records the owner; each hop records itself.
	if len(wc.GetActorChain()) == 0 {
		return fmt.Errorf("%w: a derived capability adds an actor hop, and this one has none", ErrInvalid)
	}
	hop := wc.ActorChain[len(wc.ActorChain)-1]
	attestedAny := attested.ImageDigest != "" || attested.BuildIncarnation != 0
	if bearsNone {
		if attestedAny {
			return fmt.Errorf("%w: principal %q bears no execution the issuer approves, so it cannot attest build %s incarnation %d",
				ErrInvalid, exercising, attested.ImageDigest, attested.BuildIncarnation)
		}
		// A hop bearing no execution carries none. Nothing is inherited from
		// the parent here, which is the point: a human taking over a session
		// is not running the workload's build.
		hop.ImageDigest, hop.BuildIncarnation = nil, nil
		return nil
	}
	if !attestedAny {
		return fmt.Errorf("%w: principal %q exercises build %s incarnation %d, so this derivation must attest the execution it is running; pass Execution on the input",
			ErrInvalid, exercising, digest, incarnation)
	}
	if attested.ImageDigest != digest {
		return fmt.Errorf("%w: the hop attests build %s and the issuer approves %s for principal %q",
			ErrRevoked, attested.ImageDigest, digest, exercising)
	}
	if attested.BuildIncarnation != incarnation {
		return fmt.Errorf("%w: the hop attests incarnation %d and the issuer holds %d for principal %q, so this execution has been replaced",
			ErrRevoked, attested.BuildIncarnation, incarnation, exercising)
	}
	hop.ImageDigest, hop.BuildIncarnation = &digest, &incarnation
	return nil
}

// GrantInput describes the capability an approval justifies.
type GrantInput struct {
	// Grant is the approvals engine's record of the decision. Everything
	// the hop claims is copied from it, so a capability can never assert an
	// approval the issuer did not record.
	Grant *Grant

	// TTL bounds the capability. The grant window still wins when it is
	// shorter, and so does the parent session's expiry.
	TTL time.Duration

	// Execution is what the grant's subject attests it is running, on the same
	// terms as ChildInput.Execution: required when that principal bears an
	// approved execution, refused when it bears none.
	//
	// A grant hop is the one hop that may hold authority the previous hop did
	// not, which makes attesting it MORE important rather than less: an
	// approval is exactly the capability worth minting from a build nobody
	// approved.
	Execution Execution
}

// Grant mints the capability that carries an approval: a new child session
// bound to the grant's audience, subject and request, single-use, and expiring
// no later than the grant window or the parent session.
//
// This is the one hop whose scope need not be contained in the hop before it.
// It does not touch the parent: the caller still holds its session capability
// and returns to it with the authority it always had.
//
// It takes a context because it reseals against the issuer's live state, like
// every other mint. A grant capability carrying the parent's sealed numbers
// would be born dead whenever the installation moved between the parent's
// verification and this mint, and the failure would surface in the approved
// call rather than here.
func (a *Authority) Grant(ctx context.Context, parent *Verified, in GrantInput) (string, *basev0.WorkContextV1, error) {
	if parent == nil {
		return "", nil, fmt.Errorf("%w: grant needs a verified parent", ErrInvalid)
	}
	if parent.claims().GetGrantHop() != nil {
		return "", nil, fmt.Errorf("%w: a grant capability cannot carry a second grant", ErrInvalid)
	}
	grant := in.Grant
	if err := grant.validate(); err != nil {
		return "", nil, err
	}
	if grant.Revoked {
		return "", nil, fmt.Errorf("%w: grant %q is revoked", ErrInvalid, grant.ID)
	}
	now := a.now()
	expires := now.Add(in.TTL)
	if in.TTL <= 0 || grant.NotAfter.Before(expires) {
		expires = grant.NotAfter
	}
	if !now.Before(expires) {
		return "", nil, fmt.Errorf("%w: grant %q leaves no usable window", ErrInvalid, grant.ID)
	}

	actor := parent.Actor()
	hop := &basev0.WorkActorV1{
		PrincipalId:   parent.claims().GetOwnerPrincipalId(),
		PrincipalKind: parent.claims().GetOwnerPrincipalKind(),
		DelegationId:  grant.ID,
		GrantedScopes: cloneScopes([]*basev0.WorkScopeV1{grant.Scope}),
	}
	if actor != nil {
		hop.PrincipalId = actor.GetPrincipalId()
		hop.PrincipalKind = actor.GetPrincipalKind()
		hop.AgentId = actor.AgentId
		hop.OrganizationId = actor.OrganizationId
	} else {
		setOptional(&hop.AgentId, parent.claims().GetOwnerAgentId())
	}

	// The parent is held against the issuer's current revision first. The
	// grant's own revision is what the capability carries — bumping past the
	// decision is what revokes an unspent grant — but a REVOKED parent may
	// not produce one, or a bump would be escapable by getting an approval.
	if _, err := a.carryForwardRevision(ctx, parent); err != nil {
		return "", nil, err
	}
	wc, err := a.derive(parent, grant.Audience, ReplaySingleUse, expires, grant.AuthorizationRevision)
	if err != nil {
		return "", nil, err
	}
	wc.ActorChain = append(wc.ActorChain, hop)
	wc.GrantHop = &basev0.WorkGrantHopV1{
		GrantId:       grant.ID,
		Approvers:     cloneApprovers(grant.Approvers),
		GrantedScope:  cloneScopes([]*basev0.WorkScopeV1{grant.Scope})[0],
		Subject:       grant.Subject,
		RequestDigest: grant.RequestDigest,
	}
	if err := a.deriveSeal(ctx, parent, wc, "", hop.GetPrincipalId(), in.Execution); err != nil {
		return "", nil, err
	}
	return a.seal(wc)
}

// derive copies the task identity every hop of a task carries unchanged and
// opens a new session under the parent, expiring no later than it.
//
// The copy is deep. A shallow one would leave the new capability sharing
// actor hops and scope messages with the parent's verified claims, so
// mutating the returned message would rewrite what the parent is verified to
// hold — and a later exchange off that same parent would attenuate against
// the rewritten authority.
func (a *Authority) derive(parent *Verified, audience, replay string, expires time.Time, revision uint64) (*basev0.WorkContextV1, error) {
	now := a.now()
	parentExpiry := time.Unix(parent.claims().GetExpiresAtUnix(), 0)
	if parentExpiry.Before(expires) {
		expires = parentExpiry
	}
	// A verifier accepts a capability for Skew past its expiry, so a
	// legitimately held parent can already be expired. Clamping to it would
	// mint a capability that is born dead and report success; the caller
	// would meet the failure later, in another process, as an authentication
	// error rather than an expired session.
	if !now.Before(expires) {
		return nil, fmt.Errorf("%w: parent session expired at %s; it can open no further session",
			ErrInvalid, parentExpiry.UTC().Format(time.RFC3339))
	}
	parentSession := parent.Context().GetSessionId()
	wc := cloneClaims(parent.Context())
	wc.Typ = Typ
	wc.Algorithm = Algorithm
	wc.KeyId = a.KeyID
	wc.Issuer = a.Issuer
	wc.Audience = audience
	wc.NotBeforeUnix = now.Unix()
	wc.IssuedAtUnix = now.Unix()
	wc.ExpiresAtUnix = expires.Unix()
	wc.Nonce = uuid.NewString()
	wc.AuthorizationRevision = revision
	wc.ReplayPolicy = replay
	wc.SessionId = uuid.NewString()
	wc.ParentSessionId = &parentSession
	wc.GrantHop = nil
	return wc, nil
}

// revision reads the issuer's current authorization revision.
func (a *Authority) revision(ctx context.Context, tenantID string) (uint64, error) {
	if a.Revisions == nil {
		return 0, fmt.Errorf("work context: authority has no revision source")
	}
	revision, err := a.Revisions.AuthorizationRevision(ctx, tenantID)
	if err != nil {
		return 0, fmt.Errorf("work context: authorization revision for tenant %q: %w", tenantID, err)
	}
	return revision, nil
}

func cloneScopes(scopes []*basev0.WorkScopeV1) []*basev0.WorkScopeV1 {
	if scopes == nil {
		return nil
	}
	out := make([]*basev0.WorkScopeV1, 0, len(scopes))
	for _, scope := range scopes {
		cloned, ok := proto.Clone(scope).(*basev0.WorkScopeV1)
		if !ok {
			continue
		}
		out = append(out, cloned)
	}
	return out
}

func cloneApprovers(approvers []Approver) []*basev0.WorkApproverV1 {
	out := make([]*basev0.WorkApproverV1, 0, len(approvers))
	for _, approver := range approvers {
		out = append(out, &basev0.WorkApproverV1{
			PrincipalId:   approver.PrincipalID,
			PrincipalKind: approver.Kind,
		})
	}
	return out
}

// seal validates the assembled claims against the schema and signs them. A
// capability that would not verify is never handed out.
func (a *Authority) seal(wc *basev0.WorkContextV1) (string, *basev0.WorkContextV1, error) {
	// A NIL KEY IS A REFUSAL, not a panic. ed25519.Sign panics on a key of
	// the wrong length, so an Authority assembled without one crashed the
	// caller at the last step of a mint instead of saying what was missing —
	// and every other missing input here names itself.
	if len(a.Key) != ed25519.PrivateKeySize {
		return "", nil, fmt.Errorf("work context: authority has no signing key, so it can mint nothing")
	}
	if err := protovalidate.Validate(wc); err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	// The seal and every hop's epoch used to be re-checked here, because the
	// schema did not require them. It does now — required, and the epoch gte=1
	// — so protovalidate above is the check, and duplicating it would be a
	// second rule to keep in step with the first.
	if err := checkStructure(wc); err != nil {
		return "", nil, err
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(wc)
	if err != nil {
		return "", nil, fmt.Errorf("work context: marshal: %w", err)
	}
	signature := ed25519.Sign(a.Key, payload)
	encoded := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature)
	// The SIZE, bounded by the minter and not only by the reader.
	//
	// MaxTokenSize was enforced in decodeClaims, so every reader refused an
	// oversized token and the minter happily produced one: a review minted an
	// 85,903-byte capability from a sound request, which Verify and Inspect
	// both refused. The holder then has a credential nothing accepts and no
	// way to know why, and the failure surfaces in whichever process first
	// presents it rather than at the mint that had no business issuing it.
	//
	// This is the same rule as "a minter must not emit what its own verifier
	// rejects" that the binding check already follows, applied to the one
	// bound that was only ever checked on the way in.
	if size := len(encoded); size > MaxTokenSize {
		return "", nil, fmt.Errorf("%w: this capability encodes to %d bytes and no capability over %d is accepted; it carries too many scopes, hops or identifiers to be presented",
			ErrInvalid, size, MaxTokenSize)
	}
	// THE MINTER NEVER EMITS WHAT ITS OWN DECODER REFUSES, guaranteed by
	// decoding what was just produced rather than by a second list of rules.
	//
	// protovalidate and checkStructure above are not the decoder. decodeClaims
	// also refuses unknown fields, recursively, and requires the payload to be
	// its own canonical encoding — and a caller-supplied scope carrying an
	// unknown field is PRESERVED through marshalling, so the mint happily
	// produced a token every reader rejected with "carries an unknown field at
	// authority_scopes[0]". The holder then has a credential nothing accepts
	// and no way to learn why.
	//
	// Running the real decode closes the whole class, including the next rule
	// decodeClaims grows, which a duplicated checklist here would not.
	if _, _, _, err := decodeClaims(encoded); err != nil {
		return "", nil, fmt.Errorf("%w: this capability does not decode under this Core's own rules, so it would be refused by every reader: %v",
			ErrInvalid, err)
	}
	return encoded, wc, nil
}

func setOptional(field **string, value string) {
	if value == "" {
		return
	}
	*field = &value
}

// maxTTL is the ceiling this authority mints within.
func (a *Authority) maxTTL() time.Duration {
	if a.MaxTTL <= 0 {
		return DefaultMaxTTL
	}
	// Clamped, so a MaxTTL above the absolute bound is not silently honoured.
	if a.MaxTTL > MaxTTLCeiling {
		return MaxTTLCeiling
	}
	return a.MaxTTL
}

// checkTTL bounds a requested window at both ends.
//
// The upper bound was missing: the minter checked only that the TTL was
// positive, so a misconfigured host could mint a credential valid for a month
// and every verifier would accept it for a month. Revocation reaches such a
// capability, but every revocation lever is something somebody has to pull,
// and a credential's own expiry is the one bound that needs nobody. A
// consumer found this and was adding a ceiling on its own side, which is the
// wrong side: a client-side cap bounds the clients that implement it.
//
// Grant has no call here on purpose. A grant capability's window is already
// bounded by the approval's own NotAfter and by the parent session, both of
// which are shorter than this ceiling in every case that matters, and
// clamping it again would let this default silently shorten an approval
// somebody made a decision about.
func (a *Authority) checkTTL(what string, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("%w: %s needs a positive TTL", ErrInvalid, what)
	}
	if ttl > MaxTTLCeiling {
		// The absolute bound, which no configuration raises. Without it
		// MaxTTL was a default rather than a limit.
		return fmt.Errorf("%w: %s asks for a %s window and no authority mints beyond %s, whatever Authority.MaxTTL says",
			ErrInvalid, what, ttl, MaxTTLCeiling)
	}
	if ceiling := a.maxTTL(); ttl > ceiling {
		return fmt.Errorf("%w: %s asks for a %s window and this authority mints at most %s; raise Authority.MaxTTL up to %s if that is wanted",
			ErrInvalid, what, ttl, ceiling, MaxTTLCeiling)
	}
	return nil
}
