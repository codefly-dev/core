package policy_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/workcontext"
)

// identity_e2e_test.go walks the whole scenario the canonical identity model
// exists for, with a real Ed25519 issuer and a real verifier:
//
//	1. human U starts a task
//	2. agent A works on U's authority, narrowed
//	3. A calls the tool with authority it does not hold
//	4. the gateway answers approval_required, not a denial
//	5. approver V decides
//	6. A makes that one call under a grant capability
//	7. A returns to its own session, which is unchanged
//	8. audit reconstructs U → A → (grant G, approved by V) → X, and
//	   revoking G stops it
//
// Every step asserts the artifact the next one depends on, so a regression
// lands on the step that broke rather than at the end.

const (
	issuerURL           = "https://authority.codefly.test"
	signingKeyID        = "k-1"
	tenantID            = "t-acme"
	ownerPrincipalID    = "u-antoine"
	agentPrincipalID    = "a-mind"
	agentManifestID     = "codefly.dev/mind:1.2.0"
	approverPrincipalID = "u-valerie"
	delegationID        = "d-1"
	taskIdentifier      = "task-658"
	toolboxID           = "codefly.dev/github-bot:0.1.0"
	mergeToolName       = "github.merge_pr"
	readToolName        = "github.read_pr"
	organizationID      = "org-platform"
	mergeSubject        = "repo:codefly/core"
	callDigest          = "sha256:9f1c0b"
	catalogDigest       = "sha256:catalog"
	approvalRequestID   = "ar-77"
)

type identityHarness struct {
	t         *testing.T
	authority *workcontext.Authority
	public    ed25519.PublicKey
	clock     time.Time
	revision  uint64
	grants    map[string]*workcontext.Grant
	replay    *workcontext.MemoryReplayStore
}

func newIdentityHarness(t *testing.T) *identityHarness {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	h := &identityHarness{
		t:      t,
		public: public,
		// The gateway re-checks Principal.IsExpired against the wall
		// clock, so the issuer's clock has to be the same one: a fixed
		// date would make every derived Principal read as expired.
		clock:    time.Now().Truncate(time.Second),
		revision: 3,
		grants:   map[string]*workcontext.Grant{},
	}
	h.replay = workcontext.NewMemoryReplayStore()
	h.replay.Now = func() time.Time { return h.clock }
	h.authority = &workcontext.Authority{
		Issuer:    issuerURL,
		KeyID:     signingKeyID,
		Key:       private,
		Revisions: h,
		Now:       func() time.Time { return h.clock },
	}
	return h
}

func (h *identityHarness) AuthorizationRevision(context.Context, string) (uint64, error) {
	return h.revision, nil
}

func (h *identityHarness) Grant(_ context.Context, id string) (*workcontext.Grant, error) {
	grant, known := h.grants[id]
	if !known {
		return nil, errors.New("no such grant")
	}
	return grant, nil
}

func (h *identityHarness) verify(audience, token string) *workcontext.Verified {
	h.t.Helper()
	verified, err := (&workcontext.Verifier{
		Issuer:    issuerURL,
		Audience:  audience,
		Keys:      map[string]ed25519.PublicKey{signingKeyID: h.public},
		Revisions: h,
		Replay:    h.replay,
		Grants:    h,
		Now:       func() time.Time { return h.clock },
	}).Verify(context.Background(), token)
	require.NoError(h.t, err)
	return verified
}

// resign signs a doctored claims message with the harness key, so a test can
// present a capability the minting API would refuse to produce. Verified
// cannot be assembled by hand — Verify is its only constructor — so reaching
// these cases means going through the real signer and verifier.
func (h *identityHarness) resign(wc *basev0.WorkContextV1) string {
	h.t.Helper()
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(wc)
	require.NoError(h.t, err)
	signature := ed25519.Sign(h.authority.Key, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func workScope(kind, action, resourceID string) *basev0.WorkScopeV1 {
	return &basev0.WorkScopeV1{ResourceKind: kind, Actions: []string{action}, ResourceIds: []string{resourceID}}
}

func (h *identityHarness) ownerSession() *workcontext.Verified {
	h.t.Helper()
	token, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		TenantID:           tenantID,
		OwnerPrincipalID:   ownerPrincipalID,
		OwnerPrincipalKind: policy.KindHuman,
		OrganizationID:     organizationID,
		TaskID:             taskIdentifier,
		Audience:           toolboxID,
		AuthorityScopes: []*basev0.WorkScopeV1{
			{ResourceKind: "repo", Actions: []string{"read", "write"}},
		},
		TTL: time.Hour,
	})
	require.NoError(h.t, err)
	return h.verify(toolboxID, token)
}

func (h *identityHarness) agentSession(owner *workcontext.Verified) *workcontext.Verified {
	h.t.Helper()
	token, _, err := h.authority.Child(context.Background(), owner, workcontext.ChildInput{
		PrincipalID:   agentPrincipalID,
		PrincipalKind: policy.KindAgent,
		AgentID:       agentManifestID,
		DelegationID:  delegationID,
		GrantedScopes: []*basev0.WorkScopeV1{workScope("repo", "read", "codefly/core")},
		Audience:      toolboxID,
		TTL:           30 * time.Minute,
	})
	require.NoError(h.t, err)
	return h.verify(toolboxID, token)
}

// approve is step 5: V decides, and the approvals engine records a grant
// pinned to the scope, the subject and the exact call.
func (h *identityHarness) approve(id string) *workcontext.Grant {
	grant := &workcontext.Grant{
		ID:                    id,
		Approvers:             []workcontext.Approver{{PrincipalID: approverPrincipalID, Kind: policy.KindHuman}},
		Scope:                 workScope("repo", "merge", "codefly/core"),
		Subject:               mergeSubject,
		RequestDigest:         callDigest,
		Audience:              toolboxID,
		NotAfter:              h.clock.Add(5 * time.Minute),
		AuthorizationRevision: h.revision,
	}
	h.grants[id] = grant
	return grant
}

func (h *identityHarness) grantCapability(agent *workcontext.Verified, grant *workcontext.Grant) *workcontext.Verified {
	h.t.Helper()
	token, _, err := h.authority.Grant(agent, workcontext.GrantInput{Grant: grant, TTL: time.Minute})
	require.NoError(h.t, err)
	return h.verify(toolboxID, token)
}

// grantAwarePDP is the decision point the scenario needs: merging is
// approvable rather than refused, and it is allowed only for a principal whose
// delegation chain carries the grant that approved it. Reading is always
// allowed. It is programmable enough to be the real branch under test and
// nothing more.
type grantAwarePDP struct {
	grantID string
	calls   []*policy.PDPRequest
}

func (p *grantAwarePDP) Evaluate(_ context.Context, req *policy.PDPRequest) policy.PDPDecision {
	p.calls = append(p.calls, req)
	if req.Tool != mergeToolName {
		return policy.PDPDecision{Allow: true, Reason: "role-grant: reader"}
	}
	chain, _ := req.Identity["delegation_chain"].([]map[string]any)
	for _, link := range chain {
		if link["grant_id"] == p.grantID {
			return policy.PDPDecision{Allow: true, Reason: "grant: " + p.grantID}
		}
	}
	return policy.PDPDecision{
		Allow:           false,
		RequireApproval: true,
		Reason:          "merge needs an approver",
		Approval: &basev0.ApprovalRequiredV1{
			RequestId:      approvalRequestID,
			RequestedScope: workScope("repo", "merge", "codefly/core"),
			Subject:        mergeSubject,
			RequestDigest:  callDigest,
			Audience:       toolboxID,
		},
	}
}

func TestIdentityE2E_ApprovalGrantAndResume(t *testing.T) {
	h := newIdentityHarness(t)
	pdp := &grantAwarePDP{grantID: "g-1"}
	metrics := &policy.PDPMetrics{}
	gateway := &policy.GatewayEvaluator{
		Decider:    pdp,
		Secret:     []byte("0123456789abcdef0123456789abcdef"),
		DefaultTTL: time.Minute,
		Metrics:    metrics,
	}
	ctx := context.Background()

	// 1-2. U starts the task; A acts on U's authority, narrowed to reading
	// one repo. The only identity A has is the one the capability carries.
	owner := h.ownerSession()
	agentSession := h.agentSession(owner)
	agent, err := policy.PrincipalFromWorkContext(agentSession)
	require.NoError(t, err)
	require.Equal(t, agentPrincipalID, agent.ID)
	require.Equal(t, organizationID, agent.OrgID)
	require.NotEqual(t, tenantID, agent.OrgID)
	require.Equal(t, []policy.DelegationLink{
		{PrincipalID: ownerPrincipalID, Kind: policy.KindHuman, GrantID: delegationID},
	}, agent.DelegationChain)

	// The guard that caps delegation depth must not refuse the elevated
	// call: an approval is one hop however many approvers it took.
	require.NoError(t, policy.CheckDelegationDepth(agent))

	call := policy.EvaluationInput{
		Principal:     agent,
		Toolbox:       toolboxID,
		Tool:          mergeToolName,
		Resource:      mergeSubject,
		CatalogDigest: catalogDigest,
		RequestDigest: callDigest,
	}

	// 3-4. The call stops with approval_required, not a denial. The signal
	// names exactly what a grant would have to carry.
	_, err = gateway.EvaluateAndMint(ctx, call)
	require.Error(t, err)
	require.ErrorIs(t, err, policy.ErrApprovalRequired)
	require.NotErrorIs(t, err, policy.ErrGatewayDeny)
	signal, isApproval := policy.ApprovalRequiredFrom(err)
	require.True(t, isApproval)
	require.Equal(t, approvalRequestID, signal.GetRequestId())
	require.Equal(t, callDigest, signal.GetRequestDigest())
	require.Equal(t, "merge", signal.GetRequestedScope().GetActions()[0])
	require.Equal(t, int64(1), metrics.Snapshot().RequireApprovalsTotal)

	// 5-6. V approves, and A runs that one call under the grant capability.
	grant := h.approve("g-1")
	elevated := h.grantCapability(agentSession, grant)
	granted, err := policy.PrincipalFromWorkContext(elevated)
	require.NoError(t, err)

	call.Principal = granted
	require.NoError(t, policy.CheckDelegationDepth(granted))
	result, err := gateway.EvaluateAndMint(ctx, call)
	require.NoError(t, err)
	require.Equal(t, agentPrincipalID, result.Authorization.PrincipalID)
	require.Equal(t, mergeToolName, result.Authorization.Action)
	require.Equal(t, mergeSubject, result.Authorization.Resource)
	require.Equal(t, toolboxID, result.Authorization.AudienceID)
	require.Equal(t, callDigest, result.Authorization.RequestDigest)

	// The elevated authority exists nowhere but in that capability: the
	// grant is single-use and A's own session never gained it.
	_, err = (&workcontext.Verifier{
		Issuer:    issuerURL,
		Audience:  toolboxID,
		Keys:      map[string]ed25519.PublicKey{signingKeyID: h.public},
		Revisions: h,
		Replay:    h.replay,
		Grants:    h,
		Now:       func() time.Time { return h.clock },
	}).Verify(ctx, elevated.Encoded())
	require.ErrorIs(t, err, workcontext.ErrReplayed)

	// 7. A returns to its flow with the authority it always had: reading
	// still works, merging is approvable again rather than allowed.
	resumed, err := policy.PrincipalFromWorkContext(h.verify(toolboxID, agentSession.Encoded()))
	require.NoError(t, err)
	require.Equal(t, []policy.DelegationLink{
		{PrincipalID: ownerPrincipalID, Kind: policy.KindHuman, GrantID: delegationID},
	}, resumed.DelegationChain)

	read := call
	read.Principal = resumed
	read.Tool = readToolName
	_, err = gateway.EvaluateAndMint(ctx, read)
	require.NoError(t, err)

	again := call
	again.Principal = resumed
	_, err = gateway.EvaluateAndMint(ctx, again)
	require.ErrorIs(t, err, policy.ErrApprovalRequired)

	// 8. The audit record reads U → A → (grant G, approved by V) → X.
	require.Equal(t, []policy.DelegationLink{
		{PrincipalID: ownerPrincipalID, Kind: policy.KindHuman, GrantID: delegationID},
		{
			PrincipalID: approverPrincipalID, Kind: policy.KindHuman, GrantID: grant.ID,
			Approvers: []policy.Approver{{PrincipalID: approverPrincipalID, Kind: policy.KindHuman}},
		},
	}, granted.DelegationChain)
	require.Equal(t, ownerPrincipalID, elevated.Context().GetOwnerPrincipalId())
	require.Equal(t, taskIdentifier, elevated.Context().GetTaskId())
	require.Equal(t, agentSession.Context().GetSessionId(), elevated.Context().GetParentSessionId())
	require.Equal(t, grant.ID, elevated.Context().GetGrantHop().GetGrantId())
	require.Equal(t, approverPrincipalID, elevated.Context().GetGrantHop().GetApprovers()[0].GetPrincipalId())
	require.Equal(t, callDigest, elevated.Context().GetGrantHop().GetRequestDigest())

	// ... and revoking G stops it: a capability minted from the same grant
	// no longer verifies.
	second := h.grantCapability(agentSession, grant)
	grant.Revoked = true
	_, err = (&workcontext.Verifier{
		Issuer:    issuerURL,
		Audience:  toolboxID,
		Keys:      map[string]ed25519.PublicKey{signingKeyID: h.public},
		Revisions: h,
		Replay:    workcontext.NewMemoryReplayStore(),
		Grants:    h,
		Now:       func() time.Time { return h.clock },
	}).Verify(ctx, second.Encoded())
	require.ErrorIs(t, err, workcontext.ErrRevoked)
}
