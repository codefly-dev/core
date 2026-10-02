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
