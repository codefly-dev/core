package workcontext_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/workcontext"
)

const (
	mergeTool     = "codefly.dev/github-bot:0.1.0#merge_pr"
	subject       = "pr:codefly/core#658"
	requestDigest = "sha256:9f1c0b"
	approver      = "u-valerie"
)

// approvedGrant is the record the approvals engine writes when V approves the
// one call A may not make on its own.
func (h *harness) approvedGrant(id string) *workcontext.Grant {
	grant := &workcontext.Grant{
		ID:                    id,
		Approvers:             []string{approver},
		Scope:                 scope("repo", []string{"merge"}, []string{"codefly/core"}),
		Subject:               subject,
		RequestDigest:         requestDigest,
		Audience:              mergeTool,
		NotAfter:              h.clock.Add(5 * time.Minute),
		AuthorizationRevision: h.revision,
	}
	h.grants[id] = grant
	return grant
}

// elevated runs steps 1-6: owner session, agent session, approval, grant
// capability. It returns the agent's own session and the grant capability.
func (h *harness) elevated(t *testing.T) (*workcontext.Verified, string, *workcontext.Grant) {
	t.Helper()
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)
	grant := h.approvedGrant("g-1")
	token, _, err := h.authority.Grant(agent, workcontext.GrantInput{Grant: grant, TTL: time.Minute})
	require.NoError(t, err)
	return agent, token, grant
}

func TestGrant_MintsASingleUseChildBoundToTheApproval(t *testing.T) {
	h := newHarness(t)
	agent, token, grant := h.elevated(t)

	verified := h.mustVerify(mergeTool, token)
	hop := verified.Context.GetGrantHop()

	require.Equal(t, grant.ID, hop.GetGrantId())
	require.Equal(t, []string{approver}, hop.GetApproverPrincipalIds())
	require.Equal(t, subject, hop.GetSubject())
	require.Equal(t, requestDigest, hop.GetRequestDigest())
	require.Equal(t, workcontext.ReplaySingleUse, verified.Context.GetReplayPolicy())
	require.Equal(t, mergeTool, verified.Context.GetAudience())
	require.Equal(t, agent.Context.GetSessionId(), verified.Context.GetParentSessionId())

	// The elevated hop is still the agent, holding exactly the approved
	// scope — authority the parent session never held.
	require.Equal(t, agentID, verified.Actor().GetPrincipalId())
	require.True(t, proto.Equal(grant.Scope, verified.EffectiveScopes()[0]))
	require.False(t, workcontext.ScopesAttenuate(verified.EffectiveScopes(), agent.EffectiveScopes()))
}

func TestGrant_WindowNeverOutlivesTheGrantOrTheParent(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)
	grant := h.approvedGrant("g-1")

	token, _, err := h.authority.Grant(agent, workcontext.GrantInput{Grant: grant, TTL: time.Hour})
	require.NoError(t, err)
	require.Equal(t, grant.NotAfter.Unix(), h.mustVerify(mergeTool, token).Context.GetExpiresAtUnix())

	generous := h.approvedGrant("g-2")
	generous.NotAfter = h.clock.Add(10 * time.Hour)
	token, _, err = h.authority.Grant(agent, workcontext.GrantInput{Grant: generous, TTL: time.Hour})
	require.NoError(t, err)
	require.Equal(t, agent.Context.GetExpiresAtUnix(), h.mustVerify(mergeTool, token).Context.GetExpiresAtUnix())
}

func TestGrantCapability_RejectedForAnotherTool(t *testing.T) {
	h := newHarness(t)
	_, token, _ := h.elevated(t)

	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "presented to")
}

func TestGrantCapability_RejectedOnSecondUse(t *testing.T) {
	h := newHarness(t)
	_, token, _ := h.elevated(t)

	h.mustVerify(mergeTool, token)
	_, err := h.verify(mergeTool, token)
	require.ErrorIs(t, err, workcontext.ErrReplayed)
}

// A rejection for any other reason must not burn the single use: an agent that
// presented a grant to the wrong tool can still spend it on the right one.
func TestGrantCapability_ARejectionDoesNotConsumeIt(t *testing.T) {
	h := newHarness(t)
	_, token, _ := h.elevated(t)

	_, err := h.verify(audience, token)
	require.Error(t, err)
	h.mustVerify(mergeTool, token)
}

func TestGrantCapability_RejectedWhenTheGrantIsRevoked(t *testing.T) {
	h := newHarness(t)
	_, token, grant := h.elevated(t)

	grant.Revoked = true
	_, err := h.verify(mergeTool, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
}

func TestGrantCapability_RejectedWhenTheAuthorizationRevisionIsBumped(t *testing.T) {
	h := newHarness(t)
	_, token, _ := h.elevated(t)

	h.revision++
	_, err := h.verify(mergeTool, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
}

// Shortening the window after the capability was minted closes it: the grant
// window is read from the issuer's record at every use, not frozen into the
// capability's expiry.
func TestGrantCapability_RejectedWhenTheGrantWindowIsShortened(t *testing.T) {
	h := newHarness(t)
	_, token, grant := h.elevated(t)

	grant.NotAfter = h.clock
	_, err := h.verify(mergeTool, token)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "closed at")
}

// The hop is checked against the issuer's record, so a capability cannot claim
// a subject, a scope, a request or approvers the approval never carried.
func TestGrantCapability_RejectedWhenTheHopDoesNotMatchTheIssuersRecord(t *testing.T) {
	forgeries := map[string]func(*basev0.WorkGrantHopV1, *basev0.WorkContextV1){
		"another subject": func(hop *basev0.WorkGrantHopV1, _ *basev0.WorkContextV1) {
			hop.Subject = "pr:codefly/core#1"
		},
		"another request": func(hop *basev0.WorkGrantHopV1, _ *basev0.WorkContextV1) {
			hop.RequestDigest = "sha256:000000"
		},
		"another approver": func(hop *basev0.WorkGrantHopV1, _ *basev0.WorkContextV1) {
			hop.ApproverPrincipalIds = []string{"u-nobody"}
		},
		"another scope": func(hop *basev0.WorkGrantHopV1, wc *basev0.WorkContextV1) {
			widened := scope("repo", []string{"merge"}, []string{"codefly/secrets"})
			hop.GrantedScope = widened
			wc.ActorChain[len(wc.ActorChain)-1].GrantedScopes = []*basev0.WorkScopeV1{widened}
		},
		"a grant the issuer never recorded": func(hop *basev0.WorkGrantHopV1, wc *basev0.WorkContextV1) {
			hop.GrantId = "g-invented"
			wc.ActorChain[len(wc.ActorChain)-1].DelegationId = "g-invented"
		},
	}
	for name, forge := range forgeries {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			_, token, _ := h.elevated(t)
			forged := proto.Clone(h.mustVerify(mergeTool, token).Context).(*basev0.WorkContextV1)
			forged.Nonce = "forged-" + name
			forge(forged.GrantHop, forged)

			_, err := h.verify(mergeTool, h.resign(forged))
			require.Error(t, err)
		})
	}
}

// The grant elevates the acting agent; it never hands the elevated authority
// to a different principal.
func TestVerify_RejectsAGrantHopThatSwapsTheActor(t *testing.T) {
	h := newHarness(t)
	_, token, _ := h.elevated(t)

	forged := proto.Clone(h.mustVerify(mergeTool, token).Context).(*basev0.WorkContextV1)
	forged.Nonce = "forged-actor"
	forged.ActorChain[len(forged.ActorChain)-1].PrincipalId = "a-someone-else"

	_, err := h.verify(mergeTool, h.resign(forged))
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "rather than elevating")
}

func TestVerify_RejectsAReplayableGrant(t *testing.T) {
	h := newHarness(t)
	_, token, _ := h.elevated(t)

	forged := proto.Clone(h.mustVerify(mergeTool, token).Context).(*basev0.WorkContextV1)
	forged.Nonce = "forged-replay"
	forged.ReplayPolicy = workcontext.ReplayIdempotent

	_, err := h.verify(mergeTool, h.resign(forged))
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "authorizes one call")
}

func TestGrant_RefusesAGrantApprovingMoreThanOneCall(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)
	grant := h.approvedGrant("g-1")
	grant.Scope = scope("repo", []string{"merge", "delete"}, []string{"codefly/core"})

	_, _, err := h.authority.Grant(agent, workcontext.GrantInput{Grant: grant, TTL: time.Minute})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "exactly one action on one resource")
}

func TestGrant_RefusesARevokedGrantAndAClosedWindow(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)

	revoked := h.approvedGrant("g-revoked")
	revoked.Revoked = true
	_, _, err := h.authority.Grant(agent, workcontext.GrantInput{Grant: revoked, TTL: time.Minute})
	require.ErrorIs(t, err, workcontext.ErrInvalid)

	closed := h.approvedGrant("g-closed")
	closed.NotAfter = h.clock.Add(-time.Second)
	_, _, err = h.authority.Grant(agent, workcontext.GrantInput{Grant: closed, TTL: time.Minute})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "no usable window")
}

// Step 7: the parent session is untouched by the grant, and the elevated
// capability delegates nothing further.
func TestGrant_LeavesTheParentSessionAloneAndDelegatesNothing(t *testing.T) {
	h := newHarness(t)
	agent, token, _ := h.elevated(t)
	elevated := h.mustVerify(mergeTool, token)

	resumed := h.mustVerify(audience, agent.Encoded)
	require.Equal(t, []string{"read"}, resumed.EffectiveScopes()[0].GetActions())
	require.Equal(t, []string{"codefly/core"}, resumed.EffectiveScopes()[0].GetResourceIds())
	require.Nil(t, resumed.Context.GetGrantHop())
	require.Len(t, resumed.Context.GetActorChain(), 1)

	_, _, err := h.authority.Child(elevated, workcontext.ChildInput{
		PrincipalID:   "a-sub",
		PrincipalKind: "agent",
		AgentID:       "codefly.dev/sub:1.0.0",
		DelegationID:  "d-9",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"merge"}, []string{"codefly/core"})},
		Audience:      mergeTool,
		TTL:           time.Minute,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "delegates nothing")
}
