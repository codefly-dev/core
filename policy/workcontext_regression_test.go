package policy_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/workcontext"
)

// An approval is one hop of delegation however many people had to agree to
// it. Spreading a quorum across one link per approver counted approver
// breadth as delegation depth, so the guard refused the very call the quorum
// approved — and blamed CODEFLY_MAX_DELEGATION_DEPTH for it.
func TestPrincipalFromWorkContext_AQuorumIsOneHopNotOnePerApprover(t *testing.T) {
	h := newIdentityHarness(t)
	agent := h.agentSession(h.ownerSession())
	grant := h.approve("g-quorum")
	grant.Approvers = []workcontext.Approver{
		{PrincipalID: "u-valerie", Kind: policy.KindHuman},
		{PrincipalID: "u-wren", Kind: policy.KindHuman},
		{PrincipalID: "u-xu", Kind: policy.KindHuman},
	}

	p, err := policy.PrincipalFromWorkContext(h.grantCapability(agent, grant))
	require.NoError(t, err)

	require.Len(t, p.DelegationChain, 2, "owner lent the hop; the grant is the second")
	require.NoError(t, policy.CheckDelegationDepth(p))

	quorum := p.DelegationChain[1]
	require.Equal(t, "g-quorum", quorum.GrantID)
	require.Equal(t, []policy.Approver{
		{PrincipalID: "u-valerie", Kind: policy.KindHuman},
		{PrincipalID: "u-wren", Kind: policy.KindHuman},
		{PrincipalID: "u-xu", Kind: policy.KindHuman},
	}, quorum.Approvers, "every decider is still in the audit record")
	require.Empty(t, quorum.PrincipalID, "no single principal lent a quorum grant")
}

// The tenant is not the organization: a tenant may hold several, and
// authorization is scoped per organization. Substituting one for the other
// either denies everything or matches an organization nobody granted.
func TestPrincipalFromWorkContext_OrgIsTheOrganizationNotTheTenant(t *testing.T) {
	h := newIdentityHarness(t)
	p, err := policy.PrincipalFromWorkContext(h.agentSession(h.ownerSession()))
	require.NoError(t, err)

	require.Equal(t, organizationID, p.OrgID)
	require.NotEqual(t, tenantID, p.OrgID)
	require.Equal(t, organizationID, p.AsIdentity()["principal_org_id"])
}

// An org-bridge agent acts in an organization the owner does not belong to,
// and authorization follows the actor's own.
func TestPrincipalFromWorkContext_ActorOrganizationOverridesTheTasks(t *testing.T) {
	h := newIdentityHarness(t)
	token, _, err := h.authority.Child(context.Background(), h.ownerSession(), workcontext.ChildInput{
		Execution:      workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 1},
		PrincipalID:    agentPrincipalID,
		PrincipalKind:  policy.KindAgent,
		AgentID:        agentManifestID,
		DelegationID:   delegationID,
		OrganizationID: "org-bridged",
		GrantedScopes:  []*basev0.WorkScopeV1{workScope("repo", "read", "codefly/core")},
		Audience:       toolboxID,
		TTL:            time.Minute,
	})
	require.NoError(t, err)

	p, err := policy.PrincipalFromWorkContext(h.verify(toolboxID, token))
	require.NoError(t, err)
	require.Equal(t, "org-bridged", p.OrgID)
}

// A service or agent with no organization anywhere in the capability must
// fail loudly rather than be handed the tenant id as a stand-in.
func TestPrincipalFromWorkContext_RefusesAnAgentWithNoOrganization(t *testing.T) {
	h := newIdentityHarness(t)
	agent := h.agentSession(h.ownerSession())

	stripped := proto.Clone(agent.Context()).(*basev0.WorkContextV1)
	stripped.OrganizationId = nil
	stripped.Nonce = "no-organization"

	_, err := policy.PrincipalFromWorkContext(h.verify(toolboxID, h.resign(stripped)))
	require.ErrorIs(t, err, policy.ErrPrincipalInvalid)
	require.Contains(t, err.Error(), "requires OrgID")
}

// A capability minted before the model carried a principal kind is a valid
// capability that no identity can be derived from. The error belongs here,
// naming the gap — not in the schema, where it would retroactively invalidate
// every archived capability.
func TestPrincipalFromWorkContext_RefusesACapabilityWithNoPrincipalKind(t *testing.T) {
	h := newIdentityHarness(t)
	owner := h.ownerSession()

	archived := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	archived.OwnerPrincipalKind = nil
	archived.Nonce = "archived"

	// The capability itself is still perfectly valid — that is the point of
	// keeping the field optional on the wire. Only the derivation refuses.
	_, err := policy.PrincipalFromWorkContext(h.verify(toolboxID, h.resign(archived)))
	require.ErrorIs(t, err, policy.ErrPrincipalInvalid)
	require.Contains(t, err.Error(), "names no principal kind")
}
