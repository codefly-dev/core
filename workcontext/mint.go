package workcontext

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
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
	seal, binding, err := a.sealFor(ctx, in.OwnerPrincipalID, in.InstallationID, in.OperationBindingID, in.OwnerPrincipalID)
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
	if parent.Context().GetGrantHop() != nil {
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
	if err := a.deriveSeal(ctx, parent, wc, in.OperationBindingID, in.PrincipalID); err != nil {
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
func (a *Authority) deriveSeal(ctx context.Context, parent *Verified, wc *basev0.WorkContextV1, bindingID, exercising string) error {
	if err := a.carryForwardSeal(ctx, parent.Context()); err != nil {
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
	hop.PrincipalEpoch = &epoch

	if bindingID == "" {
		// Keep the parent's binding, which carryForwardSeal has already held
		// against live state. Re-resolving it would be the restamping this
		// function exists not to do.
		return nil
	}
	inherited, err := sealOf(wc)
	if err != nil {
		return err
	}
	_, binding, err := a.sealFor(ctx, wc.GetOwnerPrincipalId(), inherited.GetInstallationId(), bindingID, exercising)
	if err != nil {
		return err
	}
	wc.OperationBinding = binding
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
	if parent.Context().GetGrantHop() != nil {
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
		PrincipalId:   parent.Context().GetOwnerPrincipalId(),
		PrincipalKind: parent.Context().GetOwnerPrincipalKind(),
		DelegationId:  grant.ID,
		GrantedScopes: cloneScopes([]*basev0.WorkScopeV1{grant.Scope}),
	}
	if actor != nil {
		hop.PrincipalId = actor.GetPrincipalId()
		hop.PrincipalKind = actor.GetPrincipalKind()
		hop.AgentId = actor.AgentId
		hop.OrganizationId = actor.OrganizationId
	} else {
		setOptional(&hop.AgentId, parent.Context().GetOwnerAgentId())
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
	if err := a.deriveSeal(ctx, parent, wc, "", hop.GetPrincipalId()); err != nil {
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
	parentExpiry := time.Unix(parent.Context().GetExpiresAtUnix(), 0)
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
	wc := proto.Clone(parent.Context()).(*basev0.WorkContextV1)
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
		out = append(out, proto.Clone(scope).(*basev0.WorkScopeV1))
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
	if err := protovalidate.Validate(wc); err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	// The schema cannot require the seal without invalidating every archived
	// capability, so the minter refuses to hand out an unsealed one. Without
	// this, a mint path that forgot to seal would produce tokens that fail far
	// away, at verification, in a process that cannot fix them.
	if _, err := sealOf(wc); err != nil {
		return "", nil, err
	}
	// Every hop must carry its own epoch, for the same reason: a hop without
	// one is a principal that cannot be revoked, and the schema cannot
	// require the field without invalidating every archived capability.
	for index, hop := range wc.GetActorChain() {
		if hop.PrincipalEpoch == nil {
			return "", nil, fmt.Errorf("%w: actor hop %d (%s) carries no epoch", ErrUnsealed, index, hop.GetPrincipalId())
		}
	}
	if err := checkStructure(wc); err != nil {
		return "", nil, err
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(wc)
	if err != nil {
		return "", nil, fmt.Errorf("work context: marshal: %w", err)
	}
	signature := ed25519.Sign(a.Key, payload)
	encoded := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature)
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
	if ceiling := a.maxTTL(); ttl > ceiling {
		return fmt.Errorf("%w: %s asks for a %s window and this authority mints at most %s; raise Authority.MaxTTL deliberately if that is wanted",
			ErrInvalid, what, ttl, ceiling)
	}
	return nil
}
