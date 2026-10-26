package workcontext

import (
	"context"
	"crypto"
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
	//
	// It holds the key MATERIAL, which is only possible when the issuer is
	// allowed to have it. An issuer whose key lives in a key-management service
	// that never exports it injects Signer instead; exactly one of the two is
	// set.
	Key ed25519.PrivateKey

	// Signer signs without the key material being here.
	//
	// It exists because "the issuer holds an ed25519.PrivateKey" is an
	// assumption about custody, not about the capability: a host whose signing
	// key lives in a cloud key-management service can reach it only through
	// that service, and taking the material as a constructor argument makes
	// that host unable to mint at all. Injecting the signing capability instead
	// leaves the algorithm and the wire format exactly where they were — this
	// is still Ed25519 over the deterministic protobuf encoding — and moves
	// only the question of who holds the key.
	//
	// Its Public() must be an Ed25519 key, because that is what a v1 capability
	// declares and what every verifier checks.
	Signer crypto.Signer

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

	// SessionID is the session this capability opens, when the ISSUER has one
	// to bind it to. Empty mints a fresh one, which is what a session no other
	// credential names should carry.
	//
	// See SessionID on ChildInput: the two are the same field on the same terms.
	SessionID string
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
		SessionId:             sessionOrFresh(in.SessionID),
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

	// SessionID is the session this hop opens, when the ISSUER has one to bind
	// it to. Empty mints a fresh one. It may not be the parent's own session:
	// a hop is a new session, never a mutation of the one it came from.
	//
	// # Why an issuer supplies it at all
	//
	// A session id is what lets a party holding SOME OTHER credential tell
	// whether this capability was minted under it. A host whose access tokens
	// carry a session can bind a capability to the session of the caller it
	// authenticated, and a party that later holds both — the access token and
	// the capability — can check that they agree without reading anything
	// that makes it act: Inspected exposes the session to a forwarding hop
	// already. Minting a fresh id every time made that binding inexpressible,
	// and a host that needed it had no answer but a second encoding.
	//
	// # The way to supply it and defeat it at the same time
	//
	// The value is the issuer's, from a session IT authenticated — never a
	// value the caller asked for. A pairing on the session holds only while
	// every session a capability carries is either the authenticated caller's
	// own or a fresh one; an issuer that signs a caller-named id lets any
	// caller mint a capability carrying another credential's session, and the
	// pairing then admits it. Core cannot detect that — both are strings and it
	// cannot know where the issuer got the bytes — so it rests on the issuer.
	// When the issuer has no authenticated session
	// to bind, leave it empty.
	SessionID string
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
	wc, err := a.derive(parent, in.Audience, replay, a.now().Add(in.TTL), revision, in.SessionID)
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
	signer, err := a.signingKey()
	if err != nil {
		return fmt.Errorf("work context: authority has no signing key, so it can derive nothing: %w", err)
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
	public, ok := signer.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("work context: this issuer's signing key is not an ed25519 key")
	}
	if !ed25519.Verify(public, payload, signature) {
		return fmt.Errorf("%w: the parent's signature does not verify under key %q as this issuer holds it now",
			ErrInvalid, a.KeyID)
	}
	return nil
}

func (a *Authority) deriveSeal(ctx context.Context, parent *Verified, wc *basev0.WorkContextV1, bindingID, exercising string) error {
	if err := a.requireTrustedParent(parent); err != nil {
		return err
	}
	if err := a.carryForwardSeal(ctx, parent.claims()); err != nil {
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
	wc, err := a.derive(parent, grant.Audience, ReplaySingleUse, expires, grant.AuthorizationRevision, "")
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
func (a *Authority) derive(parent *Verified, audience, replay string, expires time.Time, revision uint64, session string) (*basev0.WorkContextV1, error) {
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
	if session != "" && session == parentSession {
		return nil, fmt.Errorf("%w: a derived capability opens a new session and may not reuse its parent's", ErrInvalid)
	}
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
	wc.SessionId = sessionOrFresh(session)
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

// signingKey resolves the one signing capability this authority was given.
//
// Exactly one of Key and Signer may be set. Accepting both and preferring one
// would make a caller that set the wrong field sign with a key it did not mean
// to, and the capability would verify — so the ambiguity is refused instead.
func (a *Authority) signingKey() (crypto.Signer, error) {
	hasKey := len(a.Key) > 0
	switch {
	case hasKey && a.Signer != nil:
		return nil, fmt.Errorf("both Key and Signer are set; an authority has one signing capability")
	case a.Signer != nil:
		if _, ok := a.Signer.Public().(ed25519.PublicKey); !ok {
			return nil, fmt.Errorf("the injected signer's public key is not an ed25519 key")
		}
		return a.Signer, nil
	case hasKey:
		if len(a.Key) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("the signing key is %d bytes, want %d", len(a.Key), ed25519.PrivateKeySize)
		}
		return a.Key, nil
	default:
		return nil, fmt.Errorf("neither Key nor Signer is set")
	}
}

// seal validates the assembled claims against the schema and signs them. A
// capability that would not verify is never handed out.
func (a *Authority) seal(wc *basev0.WorkContextV1) (string, *basev0.WorkContextV1, error) {
	// A NIL KEY IS A REFUSAL, not a panic. ed25519.Sign panics on a key of
	// the wrong length, so an Authority assembled without one crashed the
	// caller at the last step of a mint instead of saying what was missing —
	// and every other missing input here names itself.
	signer, err := a.signingKey()
	if err != nil {
		return "", nil, fmt.Errorf("work context: authority has no signing key, so it can mint nothing: %w", err)
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
	// opts is crypto.Hash(0): Ed25519 signs the message itself, never a digest,
	// and a signer handed a hash would produce something no verifier accepts.
	signature, err := signer.Sign(nil, payload, crypto.Hash(0))
	if err != nil {
		return "", nil, fmt.Errorf("work context: sign: %w", err)
	}
	if len(signature) != ed25519.SignatureSize {
		return "", nil, fmt.Errorf("work context: the signer returned a %d-byte signature, want %d",
			len(signature), ed25519.SignatureSize)
	}
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

// sessionOrFresh is the session a capability opens: the one the issuer bound,
// or a fresh one when it bound none. The schema bounds a supplied value, so an
// empty or oversized id is refused by the same validation every field is.
func sessionOrFresh(session string) string {
	if session == "" {
		return uuid.NewString()
	}
	return session
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
