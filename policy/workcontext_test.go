package policy_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/workcontext"
)

func TestPrincipalFromWorkContext_OwnerActingDirectly(t *testing.T) {
	h := newIdentityHarness(t)
	owner := h.ownerSession()

	p, err := policy.PrincipalFromWorkContext(owner)
	require.NoError(t, err)

	require.Equal(t, ownerPrincipalID, p.ID)
	require.Equal(t, policy.KindHuman, p.Kind)
	require.Equal(t, organizationID, p.OrgID)
	require.Equal(t, owner.Encoded(), p.Token)
	require.Empty(t, p.DelegationChain)
	require.False(t, p.IsExpiredAt(h.clock))
}

// An agent can own a task outright, and then its manifest identity has to
// come from the capability too — Principal.Validate refuses an agent without
// one.
func TestPrincipalFromWorkContext_AgentOwnerCarriesItsManifestIdentity(t *testing.T) {
	h := newIdentityHarness(t)
	token, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		Execution:          workcontext.Execution{ImageDigest: policyBuildDigest(agentPrincipalID), BuildIncarnation: 1},
		InstallationID:     installation,
		TenantID:           tenantID,
		OwnerPrincipalID:   agentPrincipalID,
		OwnerPrincipalKind: policy.KindAgent,
		OwnerAgentID:       agentManifestID,
		OrganizationID:     organizationID,
		TaskID:             taskIdentifier,
		Audience:           toolboxID,
		AuthorityScopes:    []*basev0.WorkScopeV1{workScope("repo", "read", "codefly/core")},
		TTL:                time.Minute,
	})
	require.NoError(t, err)

	p, err := policy.PrincipalFromWorkContext(h.verify(toolboxID, token))
	require.NoError(t, err)
	require.Equal(t, agentPrincipalID, p.ID)
	require.Equal(t, policy.KindAgent, p.Kind)
	require.Equal(t, agentManifestID, p.AgentID)
}

func TestPrincipalFromWorkContext_DelegatedAgentLendsFromTheOwner(t *testing.T) {
	h := newIdentityHarness(t)
	agent := h.agentSession(h.ownerSession())

	p, err := policy.PrincipalFromWorkContext(agent)
	require.NoError(t, err)

	require.Equal(t, agentPrincipalID, p.ID)
	require.Equal(t, policy.KindAgent, p.Kind)
	require.Equal(t, agentManifestID, p.AgentID)
	require.Equal(t, []policy.DelegationLink{
		{PrincipalID: ownerPrincipalID, Kind: policy.KindHuman, GrantID: delegationID},
	}, p.DelegationChain)
}

func TestPrincipalFromWorkContext_ApproversAreTheLendersOfAnElevatedHop(t *testing.T) {
	h := newIdentityHarness(t)
	agent := h.agentSession(h.ownerSession())
	elevated := h.grantCapability(agent, h.approve("g-1"))

	p, err := policy.PrincipalFromWorkContext(elevated)
	require.NoError(t, err)

	// The actor is still the agent; the chain reads U → V, which is the
	// audit record the delegation example in Principal describes.
	require.Equal(t, agentPrincipalID, p.ID)
	require.Equal(t, []policy.DelegationLink{
		{PrincipalID: ownerPrincipalID, Kind: policy.KindHuman, GrantID: delegationID},
		{
			PrincipalID: approverPrincipalID, Kind: policy.KindHuman, GrantID: "g-1",
			Approvers: []policy.Approver{{PrincipalID: approverPrincipalID, Kind: policy.KindHuman}},
		},
	}, p.DelegationChain)

	identity := p.AsIdentity()
	chain := identity["delegation_chain"].([]map[string]any)
	require.Len(t, chain, 2)
	require.Equal(t, "g-1", chain[1]["grant_id"])
	require.Equal(t, []map[string]any{
		{"principal_id": approverPrincipalID, "kind": policy.KindHuman},
	}, chain[1]["approvers"])
}

func TestPrincipalFromWorkContext_RejectsAnAgentWithoutItsManifestIdentity(t *testing.T) {
	h := newIdentityHarness(t)
	owner := h.ownerSession()

	token, _, err := h.authority.Child(context.Background(), owner, workcontext.ChildInput{
		Execution:     workcontext.Execution{ImageDigest: policyBuildDigest(agentPrincipalID), BuildIncarnation: 1},
		PrincipalID:   agentPrincipalID,
		PrincipalKind: policy.KindAgent,
		DelegationID:  delegationID,
		GrantedScopes: []*basev0.WorkScopeV1{workScope("repo", "read", "codefly/core")},
		Audience:      toolboxID,
		TTL:           time.Minute,
	})
	require.NoError(t, err)

	_, err = policy.PrincipalFromWorkContext(h.verify(toolboxID, token))
	require.ErrorIs(t, err, policy.ErrPrincipalInvalid)
	require.Contains(t, err.Error(), "agent kind requires non-empty AgentID")
}

func TestPrincipalFromWorkContext_RefusesNothing(t *testing.T) {
	_, err := policy.PrincipalFromWorkContext(nil)
	require.ErrorIs(t, err, policy.ErrPrincipalInvalid)
}

// Both identity entrypoints share ONE derivation, and the Principal records
// which question was answered.
//
// The verify-only entrypoint had no way to derive an identity at all, which
// forced a gateway needing one to re-implement the delegation-chain
// derivation — the grant link, the quorum-as-one-hop rule, the actor
// override. That is a second implementation of something subtle, and it is the
// failure this model exists to prevent.
//
// Adding the entrypoint without EstablishedBy would have erased the
// distinction *Authenticated exists to carry, exactly at the policy boundary
// where it matters most. So the test asserts both halves: the chains are
// identical, and the establishment differs.
func TestBothIdentityEntrypointsShareOneDerivationAndRecordWhichAnswered(t *testing.T) {
	h := newIdentityHarness(t)
	agent := h.agentSession(h.ownerSession())
	token := agent.Encoded()

	verified, err := policy.PrincipalFromWorkContext(agent)
	require.NoError(t, err)
	require.Equal(t, policy.EstablishedByVerification, verified.EstablishedBy)

	authenticated, err := h.authenticator(toolboxID).Authenticate(context.Background(), token)
	require.NoError(t, err)
	fromAuthenticated, err := policy.PrincipalFromAuthenticatedWorkContext(authenticated)
	require.NoError(t, err)
	require.Equal(t, policy.EstablishedByAuthentication, fromAuthenticated.EstablishedBy)

	// Everything the derivation produces is identical — same id, kind, org,
	// agent, and the same delegation chain. One implementation.
	require.Equal(t, verified.ID, fromAuthenticated.ID)
	require.Equal(t, verified.Kind, fromAuthenticated.Kind)
	require.Equal(t, verified.OrgID, fromAuthenticated.OrgID)
	require.Equal(t, verified.AgentID, fromAuthenticated.AgentID)
	require.Equal(t, verified.DelegationChain, fromAuthenticated.DelegationChain)

	// And the one thing that differs is the one thing that should.
	require.NotEqual(t, verified.EstablishedBy, fromAuthenticated.EstablishedBy)

	require.Error(t, func() error {
		_, err := policy.PrincipalFromAuthenticatedWorkContext(nil)
		return err
	}())
}

// A zero value is refused by NAME, not by panicking. The first pass at this
// guarded three Verified accessors and missed the rest, so the forwarders on
// Authenticated still reached a nil *Verified through Encoded and SHA256 —
// round six called the fix incomplete and it was.
func TestAZeroCapabilityDerivesNoIdentity(t *testing.T) {
	for name, derive := range map[string]func() (*policy.Principal, error){
		"zero Authenticated": func() (*policy.Principal, error) {
			return policy.PrincipalFromAuthenticatedWorkContext(&workcontext.Authenticated{})
		},
		"zero Verified": func() (*policy.Principal, error) {
			return policy.PrincipalFromWorkContext(&workcontext.Verified{})
		},
		"nil Authenticated": func() (*policy.Principal, error) {
			return policy.PrincipalFromAuthenticatedWorkContext(nil)
		},
		"nil Verified": func() (*policy.Principal, error) {
			return policy.PrincipalFromWorkContext(nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.NotPanics(t, func() {
				principal, err := derive()
				require.Nil(t, principal)
				require.ErrorIs(t, err, policy.ErrPrincipalInvalid)
			})
		})
	}

	// And every accessor on a zero value answers rather than panicking.
	require.NotPanics(t, func() {
		zero := &workcontext.Authenticated{}
		require.Empty(t, zero.Encoded())
		require.Empty(t, zero.SHA256())
		require.Empty(t, zero.OperationBindingID())
		require.Nil(t, zero.Context())
		require.Nil(t, zero.Actor())
		require.Error(t, zero.RequireBinding("anything"))
	})
}
