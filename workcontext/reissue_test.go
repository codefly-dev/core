package workcontext_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/workcontext"
)

// A re-issue is the one derivation that hands authority to nobody: the holder is
// the principal the capability already names, and only the callee and the reach
// change. So the proof is an EQUALITY proof — the parent, field for field,
// except what a new signature for a new callee must move — plus the lineage that
// takes the place of the session it cannot keep.
//
// Every test here verifies what was minted, so "the minter wrote the field" and
// "a verifier reads it back" are one assertion.

const otherAudience = "codefly.dev/producer:0.1.0"

func (h *harness) reissue(parent *workcontext.Verified, in workcontext.ReissueInput) (string, error) {
	h.t.Helper()
	if in.Audience == "" {
		in.Audience = otherAudience
	}
	if in.TTL == 0 {
		in.TTL = 10 * time.Minute
	}
	token, _, err := h.authority.Reissue(context.Background(), parent, in)
	return token, err
}

// reissued is the shape a callee holding a re-issue to its parent proves: the
// parent with the audience, key, nonce, times, session, lineage and effective
// scopes it is allowed to move, and nothing else.
func reissued(parent, child *basev0.WorkContextV1, scopes []*basev0.WorkScopeV1) *basev0.WorkContextV1 {
	expected, _ := proto.Clone(parent).(*basev0.WorkContextV1)
	expected.Audience, expected.KeyId, expected.Nonce = child.Audience, child.KeyId, child.Nonce
	expected.NotBeforeUnix, expected.IssuedAtUnix = child.NotBeforeUnix, child.IssuedAtUnix
	expected.ExpiresAtUnix, expected.ReplayPolicy = child.ExpiresAtUnix, child.ReplayPolicy
	expected.SessionId, expected.ParentSessionId = child.SessionId, child.ParentSessionId
	if scopes != nil {
		if hops := expected.GetActorChain(); len(hops) > 0 {
			hops[len(hops)-1].GrantedScopes = scopes
		} else {
			expected.AuthorityScopes = scopes
		}
	}
	return expected
}

func TestReissue_IsTheParentRePointedAtAnotherAudience(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)

	token, err := h.reissue(agent, workcontext.ReissueInput{})
	require.NoError(t, err)
	child := h.mustVerify(otherAudience, token).Context()

	// The link the session cannot carry: the child names the session it
	// descends from, and opens one of its own.
	require.Equal(t, agent.Context().GetSessionId(), child.GetParentSessionId())
	require.NotEqual(t, agent.Context().GetSessionId(), child.GetSessionId())
	require.NotEmpty(t, child.GetSessionId())
	// NO NEW HOP, and every hop byte-identical: the chain records delegations
	// that happened, and this one did not.
	require.Len(t, child.GetActorChain(), len(agent.Context().GetActorChain()))
	require.Empty(t, cmpDiff(reissued(agent.Context(), child, nil), child))
}

func TestReissue_KeepsTheSealAndEveryHopExecution(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)

	token, err := h.reissue(agent, workcontext.ReissueInput{})
	require.NoError(t, err)
	child := h.mustVerify(otherAudience, token).Context()

	// The installation and the owner's epoch are the parent's, held against
	// live state rather than restamped — a seal is compared for equality and a
	// change to it is a revocation.
	require.True(t, proto.Equal(agent.Context().GetSeal(), child.GetSeal()))
	// The hop still attests the build the AGENT runs, not the owner's: a
	// re-issue attests nothing, because it adds no principal to attest for.
	require.Equal(t, agentBuildDigest, child.GetActorChain()[0].GetImageDigest())
	require.Equal(t, agentBuildDigest, agent.Context().GetActorChain()[0].GetImageDigest())
}

func TestReissue_AttenuatesTheEffectiveAuthorityAndRefusesWidening(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)
	narrowed := []*basev0.WorkScopeV1{scope("repo", []string{"read"}, []string{"codefly/core"})}

	token, err := h.reissue(agent, workcontext.ReissueInput{Scopes: narrowed})
	require.NoError(t, err)
	child := h.mustVerify(otherAudience, token)

	// Narrowed ON THE FINAL HOP, which is what a verifier reads. Writing it to
	// authority_scopes under a hop that still held the wider set would
	// attenuate nothing.
	require.True(t, proto.Equal(narrowed[0], child.EffectiveScopes()[0]))
	require.Empty(t, cmpDiff(reissued(agent.Context(), child.Context(), narrowed), child.Context()))

	// The ceiling is the parent's EFFECTIVE authority — the hop's read on one
	// repo — never the owner's wider grant the hop already narrowed.
	for name, widened := range map[string]*basev0.WorkScopeV1{
		"an action the parent does not hold":  scope("repo", []string{"write"}, []string{"codefly/core"}),
		"a resource the parent does not name": scope("repo", []string{"read"}, []string{"codefly/other"}),
		"every resource of the kind":          scope("repo", []string{"read"}, nil),
		"a kind the parent does not hold":     scope("secret", []string{"read"}, []string{"db"}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := h.reissue(agent, workcontext.ReissueInput{Scopes: []*basev0.WorkScopeV1{widened}})
			require.ErrorIs(t, err, workcontext.ErrInvalid)
		})
	}
}

func TestReissue_NeverOutlivesItsParent(t *testing.T) {
	h := newHarness(t)
	// About the PARENT clamp, not the minter's ceiling: the parent's expiry
	// must win even for a window this authority would otherwise mint.
	h.authority.MaxTTL = 24 * time.Hour
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)

	token, err := h.reissue(agent, workcontext.ReissueInput{TTL: 10 * time.Hour})
	require.NoError(t, err)
	child := h.mustVerify(otherAudience, token)

	require.Equal(t, agent.Context().GetExpiresAtUnix(), child.Context().GetExpiresAtUnix())
}

func TestReissue_RefusesAnExpiredOrUntrustedParent(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)

	t.Run("a parent held past its expiry opens no further session", func(t *testing.T) {
		// A verifier accepts a capability for Skew past its expiry, so a
		// legitimately held parent can already be expired. Clamping to it would
		// mint a capability born dead and report success.
		h.clock = h.clock.Add(31 * time.Minute)
		defer func() { h.clock = h.clock.Add(-31 * time.Minute) }()

		_, err := h.reissue(agent, workcontext.ReissueInput{})
		require.ErrorIs(t, err, workcontext.ErrInvalid)
		// Named, because both refusals here are ErrInvalid and an earlier guard
		// firing would let this pass for the wrong reason.
		require.ErrorContains(t, err, "can open no further session")
	})

	t.Run("a parent signed under another key is not laundered into a child", func(t *testing.T) {
		rotated := newHarness(t)
		rotated.authority.KeyID = keyID
		_, err := rotated.reissue(agent, workcontext.ReissueInput{})
		require.ErrorIs(t, err, workcontext.ErrInvalid)
		require.ErrorContains(t, err, "does not verify under key")
	})
}

func TestReissue_RefusesARevokedParent(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)
	// Advancing the agent's epoch revokes the hop. The parent would no longer
	// verify, so it re-issues nothing: a re-issue asks exactly "would this
	// parent verify right now".
	require.NoError(t, h.seals.PutEpoch(agentID, 2))

	_, err := h.reissue(agent, workcontext.ReissueInput{})
	require.ErrorIs(t, err, workcontext.ErrRevoked)
}

func TestReissue_RefusesAGrantCapability(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)
	grant := workcontext.FixtureGrant(h.clock)
	grant.Audience = audience
	grant.Subject = agentID
	grant.Scope = scope("repo", []string{"read"}, []string{"codefly/core"})
	grant.AuthorizationRevision = h.revision
	h.grants[grant.ID] = grant

	token, _, err := h.authority.Grant(context.Background(), agent, workcontext.GrantInput{
		Execution: workcontext.Execution{ImageDigest: agentBuildDigest, BuildIncarnation: 11},
		Grant:     grant, TTL: time.Minute,
	})
	require.NoError(t, err)
	granted := h.mustVerify(audience, token)

	// A grant authorizes one call at one callee. Re-pointing it at another
	// audience would spend an approval somewhere nobody approved.
	_, err = h.reissue(granted, workcontext.ReissueInput{})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
}

func TestReissue_RefusesIncompleteInput(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	for name, in := range map[string]workcontext.ReissueInput{
		"no audience":                           {Audience: " ", TTL: time.Minute},
		"no window":                             {Audience: otherAudience, TTL: -time.Minute},
		"a window past the authority's ceiling": {Audience: otherAudience, TTL: 48 * time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			if in.Audience == " " {
				in.Audience = ""
			}
			_, _, err := h.authority.Reissue(context.Background(), owner, in)
			require.ErrorIs(t, err, workcontext.ErrInvalid)
		})
	}

	_, _, err := h.authority.Reissue(context.Background(), nil, workcontext.ReissueInput{
		Audience: otherAudience, TTL: time.Minute,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
}

// An unactored parent — the owner acting directly — re-issues too, and its
// authority_scopes are what attenuate. It is the shape a re-mint from the owner
// cannot give a lineage at all, because core writes no parent link on a Start.
func TestReissue_CarriesLineageForAnUnactoredParent(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	narrowed := []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)}

	token, err := h.reissue(owner, workcontext.ReissueInput{Scopes: narrowed})
	require.NoError(t, err)
	child := h.mustVerify(otherAudience, token)

	require.Empty(t, child.Context().GetActorChain())
	require.Equal(t, owner.Context().GetSessionId(), child.Context().GetParentSessionId())
	require.True(t, proto.Equal(narrowed[0], child.EffectiveScopes()[0]))
	require.Empty(t, cmpDiff(reissued(owner.Context(), child.Context(), narrowed), child.Context()))
}

// Two re-issues of one parent are two sessions. Freshness is the property a
// holder cannot supply, and sharing a session would let one re-issue be
// mistaken for another's descendant.
func TestReissue_OpensADistinctSessionEveryTime(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	first, err := h.reissue(owner, workcontext.ReissueInput{})
	require.NoError(t, err)
	second, err := h.reissue(owner, workcontext.ReissueInput{})
	require.NoError(t, err)

	one := h.mustVerify(otherAudience, first).Context()
	two := h.mustVerify(otherAudience, second).Context()
	require.NotEqual(t, one.GetSessionId(), two.GetSessionId())
	require.Equal(t, one.GetParentSessionId(), two.GetParentSessionId())
}

// An issuer that holds authority THROUGH an audience's installation re-issues
// onto the new audience's installation, or an uninstall of the new callee would
// revoke nothing. Only the installation and its revision move.
func TestReissue_FollowsTheAudienceOntoAnotherInstallation(t *testing.T) {
	h := newHarness(t)
	const moved = "inst-producer"
	require.NoError(t, h.seals.Put(ownerID, workcontext.Seal{InstallationID: moved, InstallationRevision: 9}))
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)

	token, err := h.reissue(agent, workcontext.ReissueInput{InstallationID: moved})
	require.NoError(t, err)
	child := h.mustVerify(otherAudience, token).Context()

	require.Equal(t, moved, child.GetSeal().GetInstallationId())
	require.Equal(t, uint64(9), child.GetSeal().GetInstallationRevision())
	// The epoch and the owner's execution are the parent's: the owner is the
	// same principal running the same build, and carryForwardSeal held both
	// before anything moved.
	require.Equal(t, agent.Context().GetSeal().GetPrincipalEpoch(), child.GetSeal().GetPrincipalEpoch())
	require.Equal(t, agent.Context().GetSeal().GetImageDigest(), child.GetSeal().GetImageDigest())
	// Everything else is still the parent, so the move cannot smuggle a change.
	expected := reissued(agent.Context(), child, nil)
	expected.Seal = child.Seal
	require.Empty(t, cmpDiff(expected, child))
}

func TestReissue_RefusesAnInstallationMoveItCannotSealTo(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	t.Run("one the owner does not hold", func(t *testing.T) {
		_, err := h.reissue(owner, workcontext.ReissueInput{InstallationID: "inst-never-installed"})
		require.ErrorIs(t, err, workcontext.ErrRevoked)
		require.ErrorContains(t, err, "does not hold")
	})

	// A binding is granted WITHIN an installation, so carrying one across would
	// mint a capability every verifier refuses — as ErrRevoked, whose text says
	// the revision was superseded, sending whoever debugs it after a revocation
	// that never happened.
	t.Run("one a carried operation binding cannot travel to", func(t *testing.T) {
		const moved = "inst-producer"
		require.NoError(t, h.seals.Put(ownerID, workcontext.Seal{InstallationID: moved, InstallationRevision: 9}))
		token, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
			Execution:          workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
			InstallationID:     installation,
			OperationBindingID: bindingID,
			TenantID:           tenant,
			OwnerPrincipalID:   ownerID,
			OwnerPrincipalKind: "human",
			OrganizationID:     organization,
			TaskID:             taskID,
			Audience:           audience,
			AuthorityScopes:    []*basev0.WorkScopeV1{scope("repo", []string{"read", "write"}, nil)},
			TTL:                time.Hour,
		})
		require.NoError(t, err)
		bound := h.mustVerify(audience, token)
		require.Equal(t, bindingID, bound.OperationBindingID())

		// Within the installation it holds, the binding travels and the
		// re-issue succeeds — so the refusal above is about the move.
		_, err = h.reissue(bound, workcontext.ReissueInput{})
		require.NoError(t, err)

		_, err = h.reissue(bound, workcontext.ReissueInput{InstallationID: moved})
		require.ErrorIs(t, err, workcontext.ErrInvalid)
		require.ErrorContains(t, err, "does not travel")
	})
}

// The source is asked about ONE installation, and an answer about a different
// one would seal the capability to something nobody named. The parent's own
// installation is answered truthfully here, so carryForwardSeal passes and the
// refusal is this check's rather than an earlier one wearing the same message.
func TestReissue_RefusesASourceThatSealsSomewhereElse(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	h.authority.Seals = driftsOnTheMove{SealSource: h.seals, moved: "inst-producer"}

	_, err := h.reissue(owner, workcontext.ReissueInput{InstallationID: "inst-producer"})
	require.ErrorContains(t, err, "answered for installation")
}

// driftsOnTheMove is a real SealSource that answers truthfully about every
// installation except the one being moved to.
type driftsOnTheMove struct {
	workcontext.SealSource
	moved string
}

func (d driftsOnTheMove) Seal(ctx context.Context, principalID, installationID string) (workcontext.Seal, error) {
	if installationID == d.moved {
		return workcontext.Seal{InstallationID: "inst-somewhere-else", InstallationRevision: 3}, nil
	}
	return d.SealSource.Seal(ctx, principalID, installationID)
}

// cmpDiff reports the fields two capabilities differ on, so a failure names the
// field rather than printing two whole messages.
func cmpDiff(want, got *basev0.WorkContextV1) string {
	if proto.Equal(want, got) {
		return ""
	}
	return "want:\n" + want.String() + "\ngot:\n" + got.String()
}
