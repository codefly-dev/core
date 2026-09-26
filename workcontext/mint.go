package workcontext

import (
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
	TenantID              string                // required; first element of the task identity tuple
	OwnerPrincipalID      string                // required; whose authority the task runs on
	OwnerPrincipalKind    string                // required; human, service or agent
	OwnerAgentID          string                // required when OwnerPrincipalKind is agent
	TaskID                string                // required; survives every child session
	Audience              string                // required; the service allowed to consume this capability
	AuthorityScopes       []*basev0.WorkScopeV1 // the maximum authority the owner delegated in
	AttributionTeamIDs    []string              // optional; verified attribution snapshot
	WorkspaceID           string                // optional
	ProjectID             string                // optional; narrows WorkspaceID
	AuthorizationRevision uint64                // the issuer's revision at mint time
	ReplayPolicy          string                // "" defaults to idempotent
	TTL                   time.Duration         // required; must be positive
}

// Start mints the first capability of a task, carrying the owner's delegated
// authority and no actor hop: the owner acting directly is not dressed up as
// a delegation to itself.
func (a *Authority) Start(in StartInput) (string, *basev0.WorkContextV1, error) {
	if in.TTL <= 0 {
		return "", nil, fmt.Errorf("%w: start needs a positive TTL", ErrInvalid)
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
		AuthorizationRevision: in.AuthorizationRevision,
		ReplayPolicy:          replay,
		TenantId:              in.TenantID,
		OwnerPrincipalId:      in.OwnerPrincipalID,
		OwnerPrincipalKind:    in.OwnerPrincipalKind,
		TaskId:                in.TaskID,
		SessionId:             uuid.NewString(),
		AuthorityScopes:       in.AuthorityScopes,
		AttributionTeamIds:    in.AttributionTeamIDs,
	}
	setOptional(&wc.OwnerAgentId, in.OwnerAgentID)
	setOptional(&wc.WorkspaceId, in.WorkspaceID)
	setOptional(&wc.ProjectId, in.ProjectID)
	return a.seal(wc)
}

// ChildInput describes one delegation hop.
type ChildInput struct {
	PrincipalID   string                // required; the actor this hop delegates to
	PrincipalKind string                // required; human, service or agent
	AgentID       string                // required when PrincipalKind is agent
	DelegationID  string                // required; the delegation that produced the hop
	GrantedScopes []*basev0.WorkScopeV1 // required; must attenuate the parent's effective scopes
	Audience      string                // required; the service allowed to consume this capability
	ReplayPolicy  string                // "" defaults to idempotent
	TTL           time.Duration         // required; clamped to the parent's expiry
}

// Child exchanges a verified parent capability for a delegated one. The hop's
// scopes must attenuate the parent's effective scopes and the child expires no
// later than the parent: an exchange narrows authority or leaves it alone,
// never the reverse.
func (a *Authority) Child(parent *Verified, in ChildInput) (string, *basev0.WorkContextV1, error) {
	if parent == nil {
		return "", nil, fmt.Errorf("%w: child needs a verified parent", ErrInvalid)
	}
	if parent.Context.GetGrantHop() != nil {
		return "", nil, fmt.Errorf("%w: a grant capability authorizes one call and delegates nothing", ErrInvalid)
	}
	if len(in.GrantedScopes) == 0 {
		return "", nil, fmt.Errorf("%w: child hop %q grants no scope", ErrInvalid, in.PrincipalID)
	}
	if !ScopesAttenuate(in.GrantedScopes, parent.EffectiveScopes()) {
		return "", nil, fmt.Errorf("%w: child hop %q widens authority beyond its parent", ErrInvalid, in.PrincipalID)
	}
	if in.TTL <= 0 {
		return "", nil, fmt.Errorf("%w: child hop %q needs a positive TTL", ErrInvalid, in.PrincipalID)
	}
	replay := in.ReplayPolicy
	if replay == "" {
		replay = ReplayIdempotent
	}
	hop := &basev0.WorkActorV1{
		PrincipalId:   in.PrincipalID,
		PrincipalKind: in.PrincipalKind,
		DelegationId:  in.DelegationID,
		GrantedScopes: in.GrantedScopes,
	}
	setOptional(&hop.AgentId, in.AgentID)
	wc := a.derive(parent, in.Audience, replay, a.now().Add(in.TTL), parent.Context.GetAuthorizationRevision())
	wc.ActorChain = append(wc.ActorChain, hop)
	return a.seal(wc)
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
func (a *Authority) Grant(parent *Verified, in GrantInput) (string, *basev0.WorkContextV1, error) {
	if parent == nil {
		return "", nil, fmt.Errorf("%w: grant needs a verified parent", ErrInvalid)
	}
	if parent.Context.GetGrantHop() != nil {
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
		PrincipalId:   parent.Context.GetOwnerPrincipalId(),
		PrincipalKind: parent.Context.GetOwnerPrincipalKind(),
		DelegationId:  grant.ID,
		GrantedScopes: []*basev0.WorkScopeV1{grant.Scope},
	}
	if actor != nil {
		hop.PrincipalId = actor.GetPrincipalId()
		hop.PrincipalKind = actor.GetPrincipalKind()
		hop.AgentId = actor.AgentId
	} else {
		setOptional(&hop.AgentId, parent.Context.GetOwnerAgentId())
	}

	wc := a.derive(parent, grant.Audience, ReplaySingleUse, expires, grant.AuthorizationRevision)
	wc.ActorChain = append(wc.ActorChain, hop)
	wc.GrantHop = &basev0.WorkGrantHopV1{
		GrantId:              grant.ID,
		ApproverPrincipalIds: slices.Clone(grant.Approvers),
		GrantedScope:         grant.Scope,
		Subject:              grant.Subject,
		RequestDigest:        grant.RequestDigest,
	}
	return a.seal(wc)
}

// derive copies the task identity every hop of a task carries unchanged and
// opens a new session under the parent, expiring no later than it.
func (a *Authority) derive(parent *Verified, audience, replay string, expires time.Time, revision uint64) *basev0.WorkContextV1 {
	now := a.now()
	if parentExpiry := time.Unix(parent.Context.GetExpiresAtUnix(), 0); parentExpiry.Before(expires) {
		expires = parentExpiry
	}
	parentSession := parent.Context.GetSessionId()
	wc := &basev0.WorkContextV1{
		Typ:                   Typ,
		Algorithm:             Algorithm,
		KeyId:                 a.KeyID,
		Issuer:                a.Issuer,
		Audience:              audience,
		NotBeforeUnix:         now.Unix(),
		IssuedAtUnix:          now.Unix(),
		ExpiresAtUnix:         expires.Unix(),
		Nonce:                 uuid.NewString(),
		AuthorizationRevision: revision,
		ReplayPolicy:          replay,
		TenantId:              parent.Context.GetTenantId(),
		OwnerPrincipalId:      parent.Context.GetOwnerPrincipalId(),
		OwnerPrincipalKind:    parent.Context.GetOwnerPrincipalKind(),
		OwnerAgentId:          parent.Context.OwnerAgentId,
		TaskId:                parent.Context.GetTaskId(),
		SessionId:             uuid.NewString(),
		ParentSessionId:       &parentSession,
		AuthorityScopes:       parent.Context.GetAuthorityScopes(),
		ActorChain:            slices.Clone(parent.Context.GetActorChain()),
		AttributionTeamIds:    parent.Context.GetAttributionTeamIds(),
		WorkspaceId:           parent.Context.WorkspaceId,
		ProjectId:             parent.Context.ProjectId,
	}
	return wc
}

// seal validates the assembled claims against the schema and signs them. A
// capability that would not verify is never handed out.
func (a *Authority) seal(wc *basev0.WorkContextV1) (string, *basev0.WorkContextV1, error) {
	if err := protovalidate.Validate(wc); err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrInvalid, err)
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
