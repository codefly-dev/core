package workcontext_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/workcontext"
)

// operationSession is a session that exercises one unit of authority, so the
// binding half of the seal is populated.
func (h *harness) operationSession(aud string) (string, *workcontext.Verified) {
	h.t.Helper()
	token, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		Execution:          workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		InstallationID:     installation,
		OperationBindingID: bindingID,
		TenantID:           tenant,
		OwnerPrincipalID:   ownerID,
		OwnerPrincipalKind: "human",
		OrganizationID:     organization,
		TaskID:             taskID,
		Audience:           aud,
		AuthorityScopes:    []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		TTL:                time.Hour,
	})
	require.NoError(h.t, err)
	return token, h.mustVerify(aud, token)
}

// The seal is read from the issuer, never taken from the caller: a minter that
// accepted the numbers would hand out capabilities nothing verifies, and the
// failure would land in another process as an authentication error.
func TestStart_SealsToTheIssuersLiveState(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	seal := owner.Context().GetSeal()
	require.Equal(t, installation, seal.GetInstallationId())
	require.Equal(t, uint64(2), seal.GetPrincipalEpoch())
	require.Equal(t, uint64(3), seal.GetInstallationRevision())
	require.Equal(t, uint64(11), seal.GetBuildIncarnation())
	require.Nil(t, owner.Context().GetOperationBinding(), "this session exercises no binding")
}

// A capability is sealed to an installation, so one must be named. Without
// this a caller could mint a capability bound to nothing and every comparison
// below would compare two zeroes.
func TestStart_RefusesWithoutAnInstallation(t *testing.T) {
	h := newHarness(t)
	_, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		Execution:          workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		TenantID:           tenant,
		OwnerPrincipalID:   ownerID,
		OwnerPrincipalKind: "human",
		TaskID:             taskID,
		Audience:           audience,
		TTL:                time.Hour,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "sealed to an installation")
}

// The acceptance case from the issue: a credential sealed to installation
// revision 3 fails verification when the verifier is handed revision 4.
//
// ONE verifier is reused across the revocation, deliberately. Building a fresh
// verifier per attempt would not catch a verifier that cached live state after
// its first successful verification — which is the shape a real deployment
// reaches for, and the shape in which revocation silently stops working.
func TestVerify_RefusesACapabilitySealedToASupersededInstallationRevision(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	verifier := h.verifier(audience)
	_, err := verifier.Verify(context.Background(), token)
	require.NoError(t, err, "sound before the revocation")

	require.NoError(t, h.seals.Put(ownerID, workcontext.Seal{
		InstallationID:       installation,
		InstallationRevision: 4,
	}))

	_, err = verifier.Verify(context.Background(), token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "sealed to installation revision 3, the issuer holds 4")
}

// The same, for an actor's epoch: one verifier, sound, then revoked.
func TestVerify_RefusesADelegatedCapabilityAfterItsActorIsRevoked(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	token, _ := h.agentSession(owner, audience)

	verifier := h.verifier(audience)
	_, err := verifier.Verify(context.Background(), token)
	require.NoError(t, err, "sound before the revocation")

	// The OWNER's seal is untouched; only the actor is cut off. Without a
	// per-actor epoch the only lever would be the tenant's authorization
	// revision, which cuts off every capability of the tenant.
	require.NoError(t, h.seals.PutEpoch(agentID, 2))

	_, err = verifier.Verify(context.Background(), token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "actor hop")

	// And the owner's own session still verifies, which is the point of the
	// epoch being per principal rather than per tenant.
	ownerToken, _ := h.ownerSession(audience)
	_, err = h.verifier(audience).Verify(context.Background(), ownerToken)
	require.NoError(t, err)
}

// Exact equality, not "at least". There is no legitimate way to hold a
// capability sealed to a state that has not happened, so the shapes that
// produce one are a rolled-back installation and a forged seal.
func TestVerify_RefusesASealAheadOfTheIssuer(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	// A source that NEVER HELD revision 3, rather than one rolled back to 2.
	// Put is monotone now — lowering a sealed value would re-admit every
	// capability sealed to the earlier one — so "the issuer is behind this
	// capability" has to be built, not reached by rewinding. Which is the
	// right friction: rewinding was itself the bug in the other direction.
	behind := workcontext.NewMemorySealSource()
	require.NoError(t, behind.Put(ownerID, workcontext.Seal{
		InstallationID:       installation,
		InstallationRevision: 2,
	}))
	require.NoError(t, behind.PutEpoch(ownerID, 2))
	h.seals = behind

	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "sealed to installation revision 3, the issuer holds 2")
}

// A sealed value only ADVANCES in the source, and a withdrawal is terminal.
//
// Each sealed field is compared for EQUALITY, so moving one back re-admits
// every capability sealed to the earlier value, and clearing Revoked
// resurrects every capability the withdrawal refused. Both were writable
// before: the source would take any value a caller handed it.
func TestMemorySealSource_WritersAreMonotoneAndWithdrawalIsTerminal(t *testing.T) {
	seals := workcontext.NewMemorySealSource()
	require.NoError(t, seals.Put(ownerID, workcontext.Seal{
		InstallationID:       installation,
		InstallationRevision: 4,
	}))

	err := seals.Put(ownerID, workcontext.Seal{
		InstallationID:       installation,
		InstallationRevision: 3,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "re-admit every capability")

	// Re-recording the same revision is accepted: monotone means it does not
	// go BACK, not that it must always advance.
	require.NoError(t, seals.Put(ownerID, workcontext.Seal{
		InstallationID:       installation,
		InstallationRevision: 4,
	}))

	// The INCARNATION is monotone through its own writer now, because the
	// execution moved off the installation seal and onto the principal.
	require.NoError(t, seals.PutApprovedBuild(ownerID, workcontext.FixtureImageDigest, 11))
	err = seals.PutApprovedBuild(ownerID, workcontext.FixtureImageDigest, 10)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "re-admit every capability sealed to the earlier run")

	// A new DIGEST at an advancing incarnation is approving a new build, not a
	// rewind, so it is accepted.
	require.NoError(t, seals.PutApprovedBuild(ownerID, "sha256:"+strings.Repeat("d", 64), 12))

	// Neither half alone is an execution.
	require.ErrorIs(t, seals.PutApprovedBuild(ownerID, "", 13), workcontext.ErrInvalid)
	require.ErrorIs(t, seals.PutApprovedBuild(ownerID, workcontext.FixtureImageDigest, 0), workcontext.ErrInvalid)

	require.NoError(t, seals.PutBinding(workcontext.OperationBinding{
		ID: bindingID, PrincipalID: ownerID, InstallationID: installation,
		Revision: 4, Incarnation: 2, Revoked: true,
	}))
	err = seals.PutBinding(workcontext.OperationBinding{
		ID: bindingID, PrincipalID: ownerID, InstallationID: installation,
		Revision: 4, Incarnation: 2,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "a withdrawal is terminal")
}

// A capability derives only from ITS OWN issuer.
//
// A *Verified proves only that SOME verifier accepted the token, and a
// verifier is pinned to one issuer — so a capability another issuer minted,
// accepted by that issuer's own verifier, was a usable parent here. Measured
// before the check existed: our authority derived a child from a foreign
// issuer's parent, and the child was sealed, signed and verifiable as ours.
func TestChild_RefusesAParentFromAnotherIssuer(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	foreign := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	foreign.Issuer = "https://authority.elsewhere.test"
	token := h.resign(foreign)

	other := &workcontext.Verifier{
		Issuer: "https://authority.elsewhere.test", Audience: audience,
		Keys:      map[string]ed25519.PublicKey{keyID: h.public},
		Revisions: h, Replay: workcontext.NewMemoryReplayStore(),
		Grants: h, Seals: h.seals, Now: func() time.Time { return h.clock },
	}
	verified, err := other.Verify(context.Background(), token)
	require.NoError(t, err, "the other issuer's own verifier accepts its own capability")

	_, _, err = h.authority.Child(context.Background(), verified, workcontext.ChildInput{
		Execution:   workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		PrincipalID: agentID, PrincipalKind: "agent", AgentID: "fixture.test/agent:1.0.0",
		DelegationID:  "d-foreign",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:      audience, TTL: time.Minute,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "derives only from its own issuer")
}

// Each sealed field is its own revocation lever, and each is compared.
//
// Each lever is moved THROUGH ITS OWN WRITER, which is the correction a second
// reviewer required: the epoch cases used to move state with Put, and Put
// then updated the seal and the epoch together — so the test could not see
// that PutEpoch alone left the owner's sessions verifying. The epoch has one
// writer now and this moves it with that one.
func TestVerify_RefusesEverySealedFieldIndependently(t *testing.T) {
	// Each case carries the phrase its OWN check produces. Asserting only
	// ErrRevoked let a mutant through: with the approved build moved, deleting
	// the digest comparison still refused on the incarnation, so the case
	// passed while the check it existed for was gone.
	for name, move := range map[string]struct {
		apply   func(*harness)
		because string
	}{
		"principal epoch": {because: "is sealed to epoch", apply: func(h *harness) {
			require.NoError(h.t, h.seals.PutEpoch(ownerID, 3))
		}},
		"installation revision": {because: "installation revision", apply: func(h *harness) {
			require.NoError(h.t, h.seals.Put(ownerID, workcontext.Seal{
				InstallationID: installation, InstallationRevision: 4,
			}))
		}},
		// The incarnation moves through PutApprovedBuild now: the execution is
		// held per PRINCIPAL rather than on the installation seal, which is
		// what lets a delegated hop attest its own.
		"build incarnation": {because: "this execution has been replaced", apply: func(h *harness) {
			require.NoError(h.t, h.seals.PutApprovedBuild(ownerID, workcontext.FixtureImageDigest, 12))
		}},
		// The APPROVED BUILD. This case was missing, and a reviewer found it
		// by mutation: deleting the verifier's image-digest comparison left
		// the whole suite AND the conformance kit green. I added the check for
		// B3 and never added the lever that moves it, so the newest sealed
		// field was the only one nothing held the verifier to.
		// A conforming source cannot hold a NEW digest at the SAME incarnation
		// — that is the swap-back C8 closed — so the incarnation advances with
		// it. The digest is compared BEFORE the incarnation, so the expected
		// phrase names the build: that is what makes deleting the digest
		// comparison fail here instead of passing on the incarnation check.
		"approved build": {because: "the issuer approves", apply: func(h *harness) {
			require.NoError(h.t, h.seals.PutApprovedBuild(ownerID, "sha256:"+strings.Repeat("c", 64), 12))
		}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			token, _ := h.ownerSession(audience)
			move.apply(h)

			_, err := h.verify(audience, token)
			require.ErrorIs(t, err, workcontext.ErrRevoked)
			require.ErrorContains(t, err, move.because,
				"this case must refuse for ITS OWN reason, not for whichever check happens to fire first")
		})
	}
}

// Revoking the OWNER refuses the owner's own sessions.
//
// This is the defect the two-source epoch produced, measured before the fix:
// PutEpoch(owner, n+1) raised the epoch map and left every stored seal
// untouched, and the verifier read the stale copy out of the seal, so the
// owner's sessions kept verifying. One source, one answer.
func TestVerify_RefusesTheOwnersOwnSessionWhenTheOwnerIsRevoked(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)
	require.NotNil(t, h.mustVerify(audience, token))

	require.NoError(t, h.seals.PutEpoch(ownerID, 3))

	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "the task owner")
}

// An epoch only advances. Lowering one would un-revoke every capability the
// principal acts in, which is how the two-source defect actually bit: Put for
// a second installation overwrote the epoch with that seal's value.
func TestMemorySealSource_RefusesToLowerAnEpoch(t *testing.T) {
	seals := workcontext.NewMemorySealSource()
	require.NoError(t, seals.PutEpoch(agentID, 5))

	err := seals.PutEpoch(agentID, 2)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "un-revoke")

	// And recording an unrelated installation does not touch it.
	require.NoError(t, seals.Put(agentID, workcontext.Seal{
		InstallationID: "inst-elsewhere", InstallationRevision: 1,
	}))
	epoch, err := seals.PrincipalEpoch(context.Background(), agentID)
	require.NoError(t, err)
	require.Equal(t, uint64(5), epoch, "Put must not be a second writer of the epoch")
}

// A principal that no longer holds the installation refuses, with a message
// that says so rather than one about revisions.
func TestVerify_RefusesAnInstallationThePrincipalNoLongerHolds(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	h.seals = workcontext.NewMemorySealSource()

	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "which principal")
}

// A capability carrying no seal is not a credential. The schema does not
// require the field — a schema rule would retroactively invalidate every
// archived capability and every receipt embedding one — so this is the check
// that makes the requirement real.
func TestVerify_RefusesAnUnsealedCapability(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	unsealed := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	unsealed.Seal = nil

	_, err := h.verify(audience, h.resign(unsealed))
	require.ErrorIs(t, err, workcontext.ErrInvalid)
}

// ... and a minter cannot hand one out either, so a mint path that forgot to
// seal fails where it can be fixed rather than at a verifier it cannot reach.
func TestSeal_RefusesToSignAnUnsealedCapability(t *testing.T) {
	h := newHarness(t)
	h.authority.Seals = nil

	_, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		Execution:          workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		InstallationID:     installation,
		TenantID:           tenant,
		OwnerPrincipalID:   ownerID,
		OwnerPrincipalKind: "human",
		TaskID:             taskID,
		Audience:           audience,
		TTL:                time.Hour,
	})
	require.ErrorContains(t, err, "authority has no seal source")
}

// The operation binding is resolved by exact lookup on the sealed ID, and both
// of its counters are compared.
func TestVerify_RefusesASupersededOperationBinding(t *testing.T) {
	for name, moved := range map[string]workcontext.OperationBinding{
		"revision": {
			ID: bindingID, PrincipalID: ownerID, InstallationID: installation,
			Revision: 5, Incarnation: 1,
		},
		"incarnation": {
			ID: bindingID, PrincipalID: ownerID, InstallationID: installation,
			Revision: 4, Incarnation: 2,
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			token, verified := h.operationSession(audience)
			require.Equal(t, bindingID, verified.Context().GetOperationBinding().GetBindingId())

			require.NoError(t, h.seals.PutBinding(moved))

			_, err := h.verify(audience, token)
			require.ErrorIs(t, err, workcontext.ErrRevoked)
			require.ErrorContains(t, err, name)
		})
	}
}

// A revoked binding refuses whatever its revision: withdrawal is not a
// revision bump, and a verifier that only compared counters would keep
// accepting a capability for a binding that was taken away.
func TestVerify_RefusesARevokedOperationBinding(t *testing.T) {
	h := newHarness(t)
	token, _ := h.operationSession(audience)

	require.NoError(t, h.seals.PutBinding(workcontext.OperationBinding{
		ID: bindingID, PrincipalID: ownerID, InstallationID: installation,
		Revision: 4, Incarnation: 1, Revoked: true,
	}))

	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "is revoked")
}

// A binding the issuer does not hold at all is a refusal and not an outage: a
// capability naming a binding that does not exist is not a credential.
func TestVerify_RefusesABindingTheIssuerDoesNotHold(t *testing.T) {
	h := newHarness(t)
	token, _ := h.operationSession(audience)

	h.seals = workcontext.NewMemorySealSource()
	require.NoError(t, h.seals.Put(ownerID, workcontext.Seal{
		InstallationID:       installation,
		InstallationRevision: 3,
	}))
	// The execution has to match, or THAT is what refuses and this test would
	// be asserting the binding rule while exercising the execution rule.
	require.NoError(t, h.seals.PutApprovedBuild(ownerID, workcontext.FixtureImageDigest, 11))
	require.NoError(t, h.seals.PutEpoch(ownerID, 2))

	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "which the issuer does not hold")
}

// DERIVATION MUST NOT DEFEAT REVOCATION. A parent whose sealed state has moved
// is a revoked credential, so it can derive nothing — and in particular a
// derived child must not be stamped with the issuer's current counters while
// keeping the parent's authority.
//
// This was a real bug in an earlier revision of this package, found by
// adversarial review. `reseal` read live values and overwrote the inherited
// seal, so: verify a parent at installation revision 3, advance the
// installation to 4, call Child — and the child kept the parent's scopes,
// carried revision 4, and verified. Every revocation this package introduces
// would have been defeated by deriving once.
//
// The reasoning that produced the bug is worth recording, because it is
// plausible and because the first version of this comment got the lesson
// wrong. It claimed an asymmetry — the authorization revision is monotonic so
// re-reading it is correct, the seal is compared for equality so re-reading it
// is laundering. A second reviewer showed that was false: both are revocation
// levers, re-reading EITHER launders it, and the revision was still being
// laundered at the mint while this comment explained why that was fine. See
// carryForwardRevision.
func TestChild_RefusesToDeriveFromAParentWhoseSealHasMoved(t *testing.T) {
	for name, move := range map[string]func(*harness){
		"installation revision": func(h *harness) {
			require.NoError(h.t, h.seals.Put(ownerID, workcontext.Seal{
				InstallationID: installation, InstallationRevision: 9,
			}))
		},
		"principal epoch": func(h *harness) {
			require.NoError(h.t, h.seals.PutEpoch(ownerID, 3))
		},
		"build incarnation": func(h *harness) {
			// The incarnation moves through PutApprovedBuild now: the execution
			// is held per PRINCIPAL rather than on the installation seal, which
			// is what lets a delegated hop attest its own.
			require.NoError(h.t, h.seals.PutApprovedBuild(ownerID, workcontext.FixtureImageDigest, 12))
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			_, owner := h.ownerSession(audience)

			move(h)

			_, _, err := h.authority.Child(context.Background(), owner, workcontext.ChildInput{
				Execution:     workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
				PrincipalID:   agentID,
				PrincipalKind: "agent",
				AgentID:       "fixture.test/agent:1.0.0",
				DelegationID:  "d-1",
				GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
				Audience:      audience,
				TTL:           time.Minute,
			})
			require.ErrorIs(t, err, workcontext.ErrRevoked)
			require.ErrorContains(t, err, "cannot derive, so mint afresh")
		})
	}
}

// A parent whose installation the principal no longer holds at all derives
// nothing either, and says so rather than reporting a comparison.
func TestChild_RefusesToDeriveFromAParentWhoseInstallationIsGone(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	h.seals = workcontext.NewMemorySealSource()
	require.NoError(t, h.seals.PutEpoch(agentID, 1))
	h.authority.Seals = h.seals

	_, _, err := h.authority.Child(context.Background(), owner, workcontext.ChildInput{
		Execution:     workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		PrincipalID:   agentID,
		PrincipalKind: "agent",
		AgentID:       "fixture.test/agent:1.0.0",
		DelegationID:  "d-1",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:      audience,
		TTL:           time.Minute,
	})
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "does not hold")
}

// A grant capability is held to the same standard, and for the sharper reason:
// a grant is minted for one approved call, so deriving one from a revoked
// parent would spend an approval on a credential that was already dead.
func TestGrant_RefusesToDeriveFromAParentWhoseSealHasMoved(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)
	grant := h.approvedGrant("g-1")

	// Move the installation revision, which is inherited state a derivation
	// must not restamp. (The execution is no longer inherited: the hop attests
	// its own, so moving it is a different test.)
	require.NoError(t, h.seals.Put(ownerID, workcontext.Seal{
		InstallationID:       installation,
		InstallationRevision: 4,
	}))

	_, _, err := h.authority.Grant(context.Background(), agent, workcontext.GrantInput{
		Execution: workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		Grant:     grant, TTL: time.Minute,
	})
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "cannot derive, so mint afresh")
}

// When nothing has moved, derivation carries the parent's sealed values
// through UNCHANGED. The child is sealed to what the parent was sealed to,
// which is what makes the refusal above the only way a seal ever changes
// across a derivation.
func TestChild_CarriesTheParentsSealForwardUnchanged(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	_, child, err := h.authority.Child(context.Background(), owner, workcontext.ChildInput{
		Execution:     workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		PrincipalID:   agentID,
		PrincipalKind: "agent",
		AgentID:       "fixture.test/agent:1.0.0",
		DelegationID:  "d-1",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:      audience,
		TTL:           time.Minute,
	})
	require.NoError(t, err)

	require.Equal(t, owner.Context().GetSeal().GetInstallationId(), child.GetSeal().GetInstallationId())
	require.Equal(t, owner.Context().GetSeal().GetInstallationRevision(), child.GetSeal().GetInstallationRevision())
	require.Equal(t, owner.Context().GetSeal().GetPrincipalEpoch(), child.GetSeal().GetPrincipalEpoch())
	require.Equal(t, owner.Context().GetSeal().GetBuildIncarnation(), child.GetSeal().GetBuildIncarnation())

	// The hop carries its OWN epoch, read live — new authority for a new
	// principal, so current by construction, unlike the inherited seal.
	require.Equal(t, uint64(1), child.GetActorChain()[0].GetPrincipalEpoch())
}

// A hop may replace the binding it exercises, and the replacement's counters
// come from the issuer rather than from the caller.
func TestChild_ResolvesTheBindingItNames(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	// Granted to the HOP's principal, because the hop is what exercises it.
	require.NoError(t, h.seals.PutBinding(workcontext.OperationBinding{
		ID: "binding:alpha:read", PrincipalID: agentID, InstallationID: installation,
		Revision: 1, Incarnation: 7,
	}))

	_, child, err := h.authority.Child(context.Background(), owner, workcontext.ChildInput{
		Execution:          workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		PrincipalID:        agentID,
		PrincipalKind:      "agent",
		AgentID:            "fixture.test/agent:1.0.0",
		DelegationID:       "d-1",
		GrantedScopes:      []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:           audience,
		OperationBindingID: "binding:alpha:read",
		TTL:                time.Minute,
	})
	require.NoError(t, err)

	binding := child.GetOperationBinding()
	require.Equal(t, "binding:alpha:read", binding.GetBindingId())
	require.Equal(t, uint64(1), binding.GetRevision())
	require.Equal(t, uint64(7), binding.GetIncarnation())
}

// A grant capability carries the parent's seal forward unchanged, like any
// other derivation, and its hop carries the elevated principal's own epoch.
func TestGrant_CarriesTheParentsSealForwardUnchanged(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)
	grant := h.approvedGrant("g-1")

	_, elevated, err := h.authority.Grant(context.Background(), agent, workcontext.GrantInput{
		Execution: workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		Grant:     grant, TTL: time.Minute,
	})
	require.NoError(t, err)
	require.Equal(t, agent.Context().GetSeal().GetBuildIncarnation(), elevated.GetSeal().GetBuildIncarnation())
	require.Equal(t, agent.Context().GetSeal().GetInstallationRevision(), elevated.GetSeal().GetInstallationRevision())

	chain := elevated.GetActorChain()
	require.NotEmpty(t, chain)
	require.NotNil(t, chain[len(chain)-1].PrincipalEpoch, "the elevated hop carries its own epoch")
}

// The source is asked about one installation and one binding, and an answer
// about a different one is an error rather than a comparison nobody can read.
// A source that resolved aliases would otherwise silently answer for a
// neighbouring installation and every field below would compare the wrong two.
func TestVerify_RefusesASourceThatAnswersForSomethingElse(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	verifier := h.verifier(audience)
	verifier.Seals = answersElsewhere{}
	_, err := verifier.Verify(context.Background(), token)
	require.ErrorContains(t, err, "answered for installation")
}

// answersElsewhere is a real SealSource that answers about a different
// installation than it was asked about — the drift this check exists for.
type answersElsewhere struct{}

func (answersElsewhere) Seal(context.Context, string, string) (workcontext.Seal, error) {
	return workcontext.Seal{
		InstallationID:       "inst-somewhere-else",
		InstallationRevision: 3,
	}, nil
}

func (answersElsewhere) PrincipalEpoch(context.Context, string) (uint64, error) { return 2, nil }

func (answersElsewhere) ApprovedBuild(context.Context, string) (string, uint64, error) {
	return workcontext.FixtureImageDigest, 11, nil
}

func (answersElsewhere) OperationBinding(context.Context, string) (workcontext.OperationBinding, error) {
	return workcontext.OperationBinding{}, workcontext.ErrNoBinding
}

// A seal whose counters are zero is not a seal: zero would compare equal to a
// source that simply had nothing recorded.
func TestMemorySealSource_RefusesAZeroSeal(t *testing.T) {
	source := workcontext.NewMemorySealSource()
	require.ErrorIs(t, source.Put(ownerID, workcontext.Seal{InstallationID: installation}), workcontext.ErrInvalid)
	require.ErrorIs(t, source.Put(ownerID, workcontext.Seal{
		InstallationRevision: 1,
	}), workcontext.ErrInvalid)
	require.ErrorIs(t, source.PutBinding(workcontext.OperationBinding{ID: bindingID}), workcontext.ErrInvalid)
}

// Two installations of one principal are two seals. Conflating them would
// compare the revision of one against the other, which is a comparison that
// passes or fails for no reason a reader could reconstruct.
func TestMemorySealSource_KeepsTwoInstallationsApart(t *testing.T) {
	source := workcontext.NewMemorySealSource()
	require.NoError(t, source.Put(ownerID, workcontext.Seal{
		InstallationID: "inst-a", InstallationRevision: 1,
	}))
	require.NoError(t, source.Put(ownerID, workcontext.Seal{
		InstallationID: "inst-b", InstallationRevision: 8,
	}))

	a, err := source.Seal(context.Background(), ownerID, "inst-a")
	require.NoError(t, err)
	require.Equal(t, uint64(1), a.InstallationRevision)

	b, err := source.Seal(context.Background(), ownerID, "inst-b")
	require.NoError(t, err)
	require.Equal(t, uint64(8), b.InstallationRevision)

	_, err = source.Seal(context.Background(), "someone-else", "inst-a")
	require.ErrorIs(t, err, workcontext.ErrNoSeal)
}

// A token in another encoding is refused as a foreign format BEFORE its
// signature is checked, and with its own error.
//
// This is the regression that made the one-implementation rule necessary. A
// second implementation signed a hand-written JSON payload; both forms are
// "<payload>.<signature>" with Ed25519, so a token from one reached the other,
// failed SIGNATURE verification, and reported "signature does not verify under
// key X" — which reads like key rotation, and is what everyone investigated
// while the actual problem was two encodings.
func TestVerify_RefusesAForeignEncodingBeforeTheSignature(t *testing.T) {
	h := newHarness(t)

	// Signed with the harness key, so the signature is genuine. A verifier
	// that checked the signature first would get past it and then produce some
	// other error; one that checks it last would report a signature failure for
	// a token that was never in this format. Neither is what happens.
	payload := []byte(`{"typ":"codefly.work-context/v1","issuer":"` + issuer + `","audience":"` + audience + `"}`)
	genuine := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.authority.Key, payload))

	_, err := h.verify(audience, genuine)
	require.ErrorIs(t, err, workcontext.ErrNotACoreToken)
	require.NotErrorIs(t, err, workcontext.ErrInvalid,
		"a foreign encoding is its own diagnosis, not a member of the invalid-capability family")
	require.NotContains(t, err.Error(), "signature")

	// The ordering is the whole point: the same JSON payload with a signature
	// that does NOT verify still reports the encoding, because the encoding is
	// checked first. If this ever reports a signature failure, the regression
	// is back.
	broken := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	_, err = h.verify(audience, broken)
	require.ErrorIs(t, err, workcontext.ErrNotACoreToken)
	require.NotContains(t, err.Error(), "signature")

	// A JSON array payload is the same answer.
	array := []byte(`[{"typ":"codefly.work-context/v1"}]`)
	_, err = h.verify(audience, base64.RawURLEncoding.EncodeToString(array)+"."+
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.authority.Key, array)))
	require.ErrorIs(t, err, workcontext.ErrNotACoreToken)

	// Leading whitespace does not launder it.
	spaced := append([]byte("  \n"), payload...)
	_, err = h.verify(audience, base64.RawURLEncoding.EncodeToString(spaced)+"."+
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.authority.Key, spaced)))
	require.ErrorIs(t, err, workcontext.ErrNotACoreToken)
}

// An empty payload is NOT a foreign encoding. "Not a core token" means "this is
// another format"; widening it to cover a malformed token of no format would
// make it mean "something was wrong early", which is the vagueness it exists to
// remove.
func TestVerify_AnEmptyPayloadIsInvalidRatherThanForeign(t *testing.T) {
	h := newHarness(t)
	_, err := h.verify(audience, ".")
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.NotErrorIs(t, err, workcontext.ErrNotACoreToken)
}

// The kit core ships must pass core's own verifier; if it does not, the kit is
// wrong rather than the consumer. The conformance package drives this properly;
// this is the one assertion that belongs beside the minter.
func TestFixtures_CoverEveryFormAndBothOutcomes(t *testing.T) {
	fixtures, err := workcontext.Fixtures(time.Now())
	require.NoError(t, err)
	require.NotEmpty(t, fixtures)

	forms := map[workcontext.Form]int{}
	outcomes := map[workcontext.Outcome]int{}
	for _, fixture := range fixtures {
		require.NotEmptyf(t, fixture.Name, "every fixture is named")
		require.NotEmptyf(t, fixture.Reason, "%s says which rule decides it", fixture.Name)
		forms[fixture.Form]++
		outcomes[fixture.Outcome]++
		if fixture.Outcome == workcontext.OutcomeRejected {
			require.NotNilf(t, fixture.Err, "%s must name the sentinel its refusal matches", fixture.Name)
		} else {
			require.Nilf(t, fixture.Err, "%s is accepted, so it names no error", fixture.Name)
		}
	}
	for _, form := range []workcontext.Form{
		workcontext.FormSession, workcontext.FormOperation, workcontext.FormDelegated,
		workcontext.FormDelegatedOperation, workcontext.FormGrant, workcontext.FormForeign,
	} {
		require.NotZerof(t, forms[form], "the kit covers no %q token", form)
	}
	require.NotZero(t, outcomes[workcontext.OutcomeAccepted])
	require.NotZero(t, outcomes[workcontext.OutcomeRejected])

	// Two calls mint two sets of nonces, so two consumers running the kit
	// cannot consume each other's single-use capability.
	again, err := workcontext.Fixtures(time.Now())
	require.NoError(t, err)
	require.Len(t, again, len(fixtures))
	for index := range fixtures {
		require.Equal(t, fixtures[index].Name, again[index].Name)
		if fixtures[index].Form == workcontext.FormForeign {
			continue
		}
		require.NotEqualf(t, fixtures[index].Token, again[index].Token,
			"%s must be minted fresh", fixtures[index].Name)
	}
}

// The fixture private key is public by construction, and that has to be
// obvious rather than discovered.
func TestFixtureKeyPairIsDeterministicAndDocumentedAsPublic(t *testing.T) {
	public, private := workcontext.FixtureKeyPair()
	againPublic, againPrivate := workcontext.FixtureKeyPair()
	require.Equal(t, public, againPublic)
	require.Equal(t, private, againPrivate)
	require.Equal(t, public, private.Public())
	require.Equal(t, map[string]ed25519.PublicKey{workcontext.FixtureKeyID: public}, workcontext.FixtureKeys())
}

// CheckEncoding is exported so every other decoder in the fleet can name the
// same condition with the same error. A second decoder answering "payload is
// not a WorkContextV1" for a foreign encoding would give an operator two
// messages for one condition — the fragmentation ErrNotACoreToken exists to
// end, in miniature.
func TestCheckEncodingNamesAForeignEncodingForAnyDecoder(t *testing.T) {
	for name, payload := range map[string][]byte{
		"a JSON object":                             []byte(`{"typ":"codefly.work-context/v1"}`),
		"a JSON array":                              []byte(`[{"typ":"x"}]`),
		"whitespace then JSON":                      []byte("  \n\t{\"typ\":\"x\"}"),
		"a JSON object behind a UTF-8 BOM":          append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"a":1}`)...),
		"a JSON object behind a BOM and whitespace": append([]byte{0xEF, 0xBB, 0xBF}, []byte("  \n{\"a\":1}")...),
	} {
		t.Run(name, func(t *testing.T) {
			err := workcontext.CheckEncoding(payload)
			require.ErrorIs(t, err, workcontext.ErrNotACoreToken)
			require.NotErrorIs(t, err, workcontext.ErrInvalid)
			require.NotContains(t, err.Error(), "signature")
		})
	}

	// And it reports the same error Verify does for the same bytes, so the two
	// cannot drift into two messages.
	h := newHarness(t)
	payload := []byte(`{"typ":"codefly.work-context/v1","issuer":"` + issuer + `"}`)
	direct := workcontext.CheckEncoding(payload)
	require.Error(t, direct)

	token := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.authority.Key, payload))
	_, viaVerify := h.verify(audience, token)
	require.Equal(t, direct.Error(), viaVerify.Error())
}

// A nil return means only "not visibly another format". It is not a valid
// capability and nothing was authenticated — a caller treating nil as
// permission has skipped verification entirely, so the contract is stated here
// as well as in the doc comment.
func TestCheckEncodingAcceptsAnythingThatIsNotVisiblyForeign(t *testing.T) {
	for name, payload := range map[string][]byte{
		"empty":              {},
		"a real capability":  mustMarshal(t, newHarness(t)),
		"arbitrary bytes":    {0x08, 0x01, 0x12, 0x03, 'a', 'b', 'c'},
		"a bare quoted word": []byte(`"json string"`),
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, workcontext.CheckEncoding(payload))
		})
	}

	// The last one is the point: a JSON string is not an object or an array, so
	// this check does not claim it. It is refused later, as invalid, by the
	// schema — which is the right division, because "another format" and
	// "malformed" are different diagnoses.
	h := newHarness(t)
	_, err := h.verify(audience, base64.RawURLEncoding.EncodeToString([]byte(`"json string"`))+".AA")
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.NotErrorIs(t, err, workcontext.ErrNotACoreToken)
}

func mustMarshal(t *testing.T, h *harness) []byte {
	t.Helper()
	_, verified := h.ownerSession(audience)
	payload, _, found := strings.Cut(verified.Encoded(), ".")
	require.True(t, found)
	claims, err := base64.RawURLEncoding.DecodeString(payload)
	require.NoError(t, err)
	return claims
}

// Minting is bound to ONE EXECUTION, which the caller attests and the issuer
// checks.
//
// Before this, StartInput said nothing about which execution was asking:
// sealFor read the CURRENT build incarnation for (principal, installation) and
// stamped it, so any caller able to mint for that principal — including a pod
// from a superseded generation — received a capability sealed to the current
// execution. That is precisely the threat the build_incarnation field's own
// comment names as the thing to prevent, left wide open at the mint. And the
// credential carried no approved-build digest at all, so nothing downstream
// could tell which build an authority had been exercised by.
func TestStart_BindsTheMintToTheAttestedExecution(t *testing.T) {
	h := newHarness(t)
	start := func(e workcontext.Execution) error {
		_, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
			Execution: e, InstallationID: installation, TenantID: tenant,
			OwnerPrincipalID: ownerID, OwnerPrincipalKind: "human", TaskID: taskID,
			Audience: audience, TTL: time.Minute,
		})
		return err
	}
	sound := workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11}
	require.NoError(t, start(sound), "the approved execution mints")

	// A pod from a superseded generation: right build, stale incarnation.
	err := start(workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 10})
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "this execution has been replaced")

	// An unapproved build, right incarnation.
	err = start(workcontext.Execution{ImageDigest: "sha256:" + strings.Repeat("b", 64), BuildIncarnation: 11})
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "the issuer approves")
	require.ErrorContains(t, err, "for principal", "the approved build is keyed on the principal now, not the installation")

	// Attesting nothing at all.
	require.ErrorIs(t, start(workcontext.Execution{}), workcontext.ErrInvalid)

	// And the credential CARRIES the approved build digest.
	token, _ := h.ownerSession(audience)
	inspected, err := workcontext.Inspect(token)
	require.NoError(t, err)
	require.Equal(t, workcontext.FixtureImageDigest, inspected.Seal().GetImageDigest())
}

// A revision AHEAD of the issuer's is refused, exactly as a seal ahead of it
// is. The package already made this argument for the seal — "there is no
// legitimate way to hold one" — and applied it to one lever but not the other.
func TestVerify_RefusesARevisionAheadOfTheIssuer(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	forged := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	forged.AuthorizationRevision = h.revision + 5
	_, err := h.verify(audience, h.resign(forged))
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "issuer is at")
}

// The use site can demand the binding that gates it.
//
// The binding half of the revocation predicate was unreachable from a call:
// the verifier checks a sealed binding thoroughly, but only when the
// capability carries one, so a capability with the same scopes and NO binding
// passed everything and revoking the binding did not reach it. Core cannot
// require a binding on every capability without collapsing sessions into
// operation contexts, so it gives the place that knows the gate a way to say
// so.
func TestRequireBinding_IsTheUseSitesHalfOfTheBindingCheck(t *testing.T) {
	h := newHarness(t)

	// An operation context exercising the binding the call is gated by.
	_, operation := h.operationSession(audience)
	require.NoError(t, operation.RequireBinding(bindingID))
	require.Equal(t, bindingID, operation.OperationBindingID())

	// The same capability is refused for a call gated by another binding.
	err := operation.RequireBinding("binding:alpha:administer")
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "exercises")

	// A plain session carries the owner's authority and exercises no binding.
	// It verifies — it is a sound capability — and it is refused for a gated
	// call, which is the gap this closes.
	_, session := h.ownerSession(audience)
	require.Empty(t, session.OperationBindingID())
	err = session.RequireBinding(bindingID)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "revoking that binding would not reach it")

	// A gate with no id is a programming error, not a pass.
	require.Error(t, operation.RequireBinding(""))
}

// A production-shaped verifier REFUSES the conformance fixture key.
//
// The kit's private key is derivable from this package's source by anyone, and
// any binary importing workcontext links it; conformance.Verifier() is an
// exported, ready-made verifier that trusts it. So one mistaken call, or one
// JWKS document that picked the fixture key up, would have made a real
// verifier accept tokens anybody can mint. It now refuses unless a field
// nobody sets in production says otherwise.
func TestVerify_RefusesTheConformanceFixtureKeyUnlessToldOtherwise(t *testing.T) {
	now := time.Now()
	fixtures, err := workcontext.Fixtures(now)
	require.NoError(t, err)
	var sound string
	for _, fixture := range fixtures {
		if fixture.Name == "session" {
			sound = fixture.Token
		}
	}
	require.NotEmpty(t, sound)

	clock := func() time.Time { return now }
	production := &workcontext.Verifier{
		// Everything the kit's own verifier has, EXCEPT the opt-in.
		Issuer:    workcontext.FixtureIssuer,
		Audience:  workcontext.FixtureAudience,
		Keys:      workcontext.FixtureKeys(),
		Revisions: workcontext.FixtureRevisions(),
		Replay:    workcontext.NewMemoryReplayStore(),
		Grants:    workcontext.FixtureGrants(now),
		Seals:     workcontext.FixtureSeals(),
		Now:       clock,
	}
	_, err = production.Verify(context.Background(), sound)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "conformance fixture key")

	// With the opt-in, the same verifier accepts it — so the refusal is the
	// key check and not something else about this configuration.
	production.TrustTheConformanceFixtureKey = true
	_, err = production.Verify(context.Background(), sound)
	require.NoError(t, err)
}

// A held *Verified cannot be edited into validity. Its claims are copies.
//
// Reproduced before the fix, by a reviewer and then by me:
//
//	seals.PutEpoch(owner, 3)               // revoke the owner
//	verifier.Verify(ctx, token)            // ErrRevoked, correctly
//	held.Context().Seal.PrincipalEpoch = 3 // edit the claims in place
//	verifier.Recheck(ctx, held)            // nil
//	authority.Child(ctx, held, ...)        // derived a VERIFYING child
//
// Every live check reads the claims back out of the Verified it was handed, so
// a caller able to edit them could move the capability onto whatever state the
// issuer currently holds. The signature over the encoded token was never in
// question; what the checks compared against was. This is the same defect
// solutionhost.Delivered had one package over, and I fixed that one first
// without looking here.
func TestAHeldVerifiedCannotBeEditedIntoValidity(t *testing.T) {
	h := newHarness(t)
	token, owner := h.ownerSession(audience)
	verifier := h.verifier(audience)

	require.NoError(t, h.seals.PutEpoch(ownerID, 3))
	_, err := verifier.Verify(context.Background(), token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)

	// Edit the held claims to match the issuer's new state.
	owner.Context().Seal.PrincipalEpoch = 3
	owner.Context().AuthorizationRevision = 999

	require.ErrorIs(t, verifier.Recheck(context.Background(), owner), workcontext.ErrRevoked)
	_, _, err = h.authority.Child(context.Background(), owner, workcontext.ChildInput{
		Execution:   workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		PrincipalID: agentID, PrincipalKind: "agent", AgentID: "fixture.test/agent:1.0.0",
		DelegationID:  "d-immutable",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:      audience, TTL: time.Minute,
	})
	require.ErrorIs(t, err, workcontext.ErrRevoked)

	// And each accessor hands back a fresh value, so one caller's edit is
	// invisible to the next.
	require.Equal(t, uint64(2), owner.Context().GetSeal().GetPrincipalEpoch())
	first := owner.EffectiveScopes()
	if len(first) > 0 {
		first[0].ResourceKind = "mutated"
		require.NotEqual(t, "mutated", owner.EffectiveScopes()[0].GetResourceKind())
	}
	if actor := owner.Actor(); actor != nil {
		actor.PrincipalId = "mutated"
		require.NotEqual(t, "mutated", owner.Actor().GetPrincipalId())
	}
}

// TestABindingDoesNotTravelWithADelegation holds the rule that a binding is
// held by a principal rather than carried by a session.
//
// The minter used to keep the parent's binding for any hop that named none.
// For a hop that is the SAME principal that is correct and is what the
// no-restamping rule requires. For a new principal it minted a capability
// every Verify refuses — and refuses as ErrRevoked, whose text reads
// "authorization revision superseded", so the failure arrives in another
// process looking like a revocation nobody performed. A minter emitting what
// its own verifier rejects is the defect; the misleading sentinel is why it
// would have cost someone an afternoon.
//
// Dropping the binding instead would have been worse and is the reason this
// refuses rather than sanitizes: with no binding, checkOperationBindingAgainst
// returns before every check it performs, so the derivation would have widened
// authority by shedding the unit it was bound to.
func TestABindingDoesNotTravelWithADelegation(t *testing.T) {
	h := newHarness(t)
	_, owner := h.operationSession(audience)
	require.Equal(t, bindingID, owner.Context().GetOperationBinding().GetBindingId())

	child := workcontext.ChildInput{
		Execution:     workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		PrincipalID:   agentID,
		PrincipalKind: "agent",
		AgentID:       "fixture.test/agent:1.0.0",
		DelegationID:  "d-1",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:      audience,
		TTL:           30 * time.Minute,
	}

	// Naming no binding, for a principal that holds none: refused at the
	// mint, by the issuer, naming the input that resolves it.
	_, _, err := h.authority.Child(context.Background(), owner, child)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "does not travel with a delegation")
	require.Contains(t, err.Error(), "OperationBindingID")

	// Naming one the hop does hold: minted, and it verifies. This is the half
	// that makes the refusal above a correction rather than a prohibition.
	require.NoError(t, h.seals.PutBinding(workcontext.OperationBinding{
		ID: "binding:alpha:agent-read", PrincipalID: agentID, InstallationID: installation,
		Revision: 1, Incarnation: 1,
	}))
	held := child
	held.OperationBindingID = "binding:alpha:agent-read"
	token, _, err := h.authority.Child(context.Background(), owner, held)
	require.NoError(t, err)
	verified := h.mustVerify(audience, token)
	require.Equal(t, "binding:alpha:agent-read", verified.OperationBindingID())

	// And a hop for the SAME principal still inherits, because the binding is
	// genuinely held by whoever exercises it.
	require.NoError(t, h.seals.PutEpoch(ownerID, 2))
	self := child
	self.PrincipalID = ownerID
	self.PrincipalKind = "human"
	self.AgentID = ""
	selfToken, _, err := h.authority.Child(context.Background(), owner, self)
	require.NoError(t, err)
	require.Equal(t, bindingID, h.mustVerify(audience, selfToken).OperationBindingID())
}

// TestADerivationAttestsItsOwnExecution is the blocker C1 named: minting was
// execution-bound only at Start.
//
// A derivation inherited the parent's execution and attested nothing, so a
// caller holding a parent capability minted children whatever IT was running.
// The old comment called that "the same execution continuing", which is true
// only when the hop is the same workload; for a new principal it was an
// assumption with nothing behind it. Worse, the execution was read from the
// OWNER's installation record, and a delegated hop's principal does not hold
// the owner's installation at all — so there was nothing a derivation could
// have been attested against even if it had tried.
func TestADerivationAttestsItsOwnExecution(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	child := workcontext.ChildInput{
		PrincipalID: agentID, PrincipalKind: "agent", AgentID: "fixture.test/agent:1.0.0",
		DelegationID: "d-1", Audience: audience, TTL: 30 * time.Minute,
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
	}

	// Attesting nothing, for a principal the issuer holds a build for: refused
	// at the mint, naming the input.
	_, _, err := h.authority.Child(context.Background(), owner, child)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "must attest the execution it is running")

	// Attesting a build the issuer does not approve FOR THIS HOP.
	wrongBuild := child
	wrongBuild.Execution = workcontext.Execution{
		ImageDigest: "sha256:" + strings.Repeat("e", 64), BuildIncarnation: 11,
	}
	_, _, err = h.authority.Child(context.Background(), owner, wrongBuild)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "the issuer approves")

	// A pod from a superseded generation: right build, stale incarnation. This
	// is the case the whole mechanism exists for, now reachable at a hop.
	// The agent's approved run advances under it.
	require.NoError(t, h.seals.PutApprovedBuild(agentID, workcontext.FixtureImageDigest, 12))
	superseded := child
	superseded.Execution = workcontext.Execution{
		ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11,
	}
	_, _, err = h.authority.Child(context.Background(), owner, superseded)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "this execution has been replaced")

	// And the sound hop mints, carrying ITS OWN execution rather than the
	// owner's — which is the half that makes the refusals above a correction
	// rather than a prohibition.
	sound := child
	sound.Execution = workcontext.Execution{
		ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 12,
	}
	token, claims, err := h.authority.Child(context.Background(), owner, sound)
	require.NoError(t, err)
	// THE HOP carries the hop's execution; the SEAL still carries the owner's.
	// One slot on the seal was the N1 regression: a derivation overwrote the
	// owner's execution with the last hop's, so superseding the owner's build
	// refused the owner's own capability and every child of it verified.
	require.Len(t, claims.GetActorChain(), 1)
	require.Equal(t, uint64(12), claims.GetActorChain()[0].GetBuildIncarnation(),
		"the hop records its own run")
	require.Equal(t, uint64(11), claims.GetSeal().GetBuildIncarnation(),
		"and the owner's is untouched, so replacing it still revokes this child")
	require.NotNil(t, h.mustVerify(audience, token))
}

// TestAHumanSessionBearsNoExecution is the human-session half of C2.
//
// sealFor required a non-empty image digest and a non-zero incarnation for
// EVERY mint, and the schema required the digest outright. A person at a
// terminal runs no approved build, so every human session had to invent a
// value — and an invented value is precisely what this field exists to refuse.
//
// The requirement is not weakened, it is made conditional on something the
// issuer knows and the schema cannot: ApprovedBuild answers whether a
// principal bears an execution at all. The correspondence is enforced in BOTH
// directions, which is what keeps the optionality honest rather than a hedge.
func TestAHumanSessionBearsNoExecution(t *testing.T) {
	h := newHarness(t)
	const human = "person-ada"
	require.NoError(t, h.seals.Put(human, workcontext.Seal{
		InstallationID: installation, InstallationRevision: 3,
	}))
	require.NoError(t, h.seals.PutEpoch(human, 1))
	// Bearing no execution is RECORDED, explicitly. This test used to record
	// nothing and say that was how it is expressed — which made "the issuer
	// has no record" and "this principal is a person" the same answer, so a
	// service principal with a missing build record was minted a capability
	// carrying no execution that every verifier accepted. An unknown
	// principal is a refusal now; only this is "bears none".
	require.NoError(t, h.seals.PutBearsNoExecution(human))

	start := func(e workcontext.Execution) (string, error) {
		token, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
			Execution: e, InstallationID: installation, TenantID: tenant,
			OwnerPrincipalID: human, OwnerPrincipalKind: "human", TaskID: taskID,
			OrganizationID: organization, Audience: audience, TTL: time.Minute,
			AuthorityScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		})
		return token, err
	}

	// A human mints attesting nothing, and the capability carries no execution.
	token, err := start(workcontext.Execution{})
	require.NoError(t, err)
	verified := h.mustVerify(audience, token)
	require.Empty(t, verified.Context().GetSeal().GetImageDigest())
	require.Zero(t, verified.Context().GetSeal().GetBuildIncarnation())

	// The OTHER direction, which is the half that makes this a rule rather
	// than a gap: a principal bearing no execution cannot CLAIM one. Without
	// this check a process could present itself as a workload and be sealed to
	// a build nobody approved for it.
	_, err = start(workcontext.Execution{
		ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "bears no execution")

	// And a verifier refuses a human's capability that has acquired one.
	forged := proto.Clone(verified.Context()).(*basev0.WorkContextV1)
	digest, incarnation := workcontext.FixtureImageDigest, uint64(11)
	forged.Seal.ImageDigest, forged.Seal.BuildIncarnation = &digest, &incarnation
	forged.Nonce = "n-forged"
	_, err = h.verify(audience, h.resign(forged))
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "bears no execution")
}

// TestTheSealSourcesWritersRefuseEveryRewind covers the writers whose guards
// had no committed test — W4 and W5 in a round-four review, which found them
// deletable with the whole suite green.
//
// A guard no test holds is indistinguishable from one that was never written,
// and these are the writers that make "compared for equality" safe: every
// sealed number is compared exactly, so a source that moves one BACK re-admits
// every capability sealed to the earlier value.
func TestTheSealSourcesWritersRefuseEveryRewind(t *testing.T) {
	source := workcontext.NewMemorySealSource()
	require.NoError(t, source.PutBinding(workcontext.OperationBinding{
		ID: bindingID, PrincipalID: ownerID, InstallationID: installation,
		Revision: 4, Incarnation: 2,
	}))

	// W4: the binding's revision only advances.
	err := source.PutBinding(workcontext.OperationBinding{
		ID: bindingID, PrincipalID: ownerID, InstallationID: installation,
		Revision: 3, Incarnation: 2,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "re-admit")

	// W5: and so does its incarnation, separately — a binding withdrawn and
	// re-created is not the same binding re-revised.
	err = source.PutBinding(workcontext.OperationBinding{
		ID: bindingID, PrincipalID: ownerID, InstallationID: installation,
		Revision: 4, Incarnation: 1,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "re-admit")

	// Revocation is TERMINAL: clearing it would resurrect every capability
	// the withdrawal refused.
	require.NoError(t, source.PutBinding(workcontext.OperationBinding{
		ID: bindingID, PrincipalID: ownerID, InstallationID: installation,
		Revision: 4, Incarnation: 2, Revoked: true,
	}))
	err = source.PutBinding(workcontext.OperationBinding{
		ID: bindingID, PrincipalID: ownerID, InstallationID: installation,
		Revision: 5, Incarnation: 2,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)

	// C8: a digest change at a FIXED incarnation is a rewind in disguise.
	// Approve B at 5, approve A again at 5, and every capability sealed to A
	// at 5 that the move to B revoked verifies again. This guard was added
	// with a comment arguing the digest "may change freely"; the comment was
	// wrong and the counterexample is two writes.
	require.NoError(t, source.PutApprovedBuild(ownerID, workcontext.FixtureImageDigest, 5))
	err = source.PutApprovedBuild(ownerID, "sha256:"+strings.Repeat("b", 64), 5)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "a new build is a new run")
	// With the incarnation advancing, approving a different build is fine.
	require.NoError(t, source.PutApprovedBuild(ownerID, "sha256:"+strings.Repeat("b", 64), 6))
	// And swapping back now needs another advance, so A at 6 is refused.
	require.ErrorIs(t, source.PutApprovedBuild(ownerID, workcontext.FixtureImageDigest, 6), workcontext.ErrInvalid)
}

// TestAHeldActorCannotBeEditedIntoWiderAuthority is W7, which had no committed
// test: Verified.Actor() handed out the live message, so an edited Actor()
// widened a child minted from that parent.
//
// Context() and EffectiveScopes() were deep-copied for exactly this reason in
// an earlier round and Actor() was missed — the same defect, one accessor over,
// which is the pattern that keeps recurring here.
func TestAHeldActorCannotBeEditedIntoWiderAuthority(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)

	actor := agent.Actor()
	require.NotNil(t, actor)
	actor.PrincipalId = "someone-else"
	actor.GrantedScopes = []*basev0.WorkScopeV1{scope("repo", []string{"read", "write", "admin"}, nil)}

	require.Equal(t, agentID, agent.Actor().GetPrincipalId(),
		"a held Actor() is a copy; editing it changes nothing the verifier or the minter will read")
	require.Equal(t, []string{"read"}, agent.Actor().GetGrantedScopes()[0].GetActions())

	// And the edit cannot widen a derivation either.
	_, claims, err := h.authority.Child(context.Background(), agent, workcontext.ChildInput{
		Execution:   workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		PrincipalID: "a-sub", PrincipalKind: "agent", AgentID: "fixture.test/sub:1.0.0",
		DelegationID: "d-2", Audience: audience, TTL: 10 * time.Minute,
		// Exactly what the parent holds: the point is the aliasing, so the
		// derivation must not fail attenuation for an unrelated reason.
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, []string{"codefly/core"})},
	})
	require.NoError(t, err)
	for _, hop := range claims.GetActorChain() {
		require.NotEqual(t, "someone-else", hop.GetPrincipalId())
		for _, granted := range hop.GetGrantedScopes() {
			require.NotContains(t, granted.GetActions(), "admin")
		}
	}
}

// TestTheMintRefusesACapabilityTooLargeToPresent is C9: Authority.seal had no
// size bound, so a sound request minted a token every reader refuses.
//
// MaxTokenSize lived only in decodeClaims — on the way IN. A review minted an
// 85,903-byte capability that both Verify and Inspect rejected, which leaves a
// holder with a credential nothing accepts and no way to learn why, failing in
// whichever process first presents it rather than at the mint. Same rule as
// "a minter must not emit what its own verifier rejects", applied to the one
// bound that was only checked on the way in.
func TestTheMintRefusesACapabilityTooLargeToPresent(t *testing.T) {
	h := newHarness(t)
	var scopes []*basev0.WorkScopeV1
	for index := 0; index < 400; index++ {
		scopes = append(scopes, scope("repo", []string{"read"},
			[]string{strings.Repeat("r", 100) + string(rune('a'+index%26))}))
	}
	_, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		Execution:      workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		InstallationID: installation, TenantID: tenant, OwnerPrincipalID: ownerID,
		OwnerPrincipalKind: "human", OrganizationID: organization, TaskID: taskID,
		Audience: audience, AuthorityScopes: scopes, TTL: time.Minute,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "too many scopes, hops or identifiers")
}

// TestSupersedingAnyLinksBuildRevokesTheWholeChain is N1, a regression my own
// C1 fix introduced and the reason an execution lives on every link.
//
// WorkSealV1 had ONE execution slot, and a derivation wrote the hop's
// execution into it. So the OWNER's execution stopped being recorded the
// moment anything was derived: superseding the owner's build refused the
// owner's own capability (ErrRevoked) while every child of it verified, and
// those children went on minting grandchildren. At the commit before the C1
// fix the same child WAS refused, so this is strictly a regression.
//
// Why it matters beyond tidiness: a host bumps build_incarnation per applied
// generation, so supersession IS how a rollout revokes. A superseded pod could
// pre-mint delegations that outlive its own replacement.
//
// The fix mirrors principal_epoch exactly — the owner's on the seal, each
// hop's on its own WorkActorV1, every one of them checked in one loop.
func TestSupersedingAnyLinksBuildRevokesTheWholeChain(t *testing.T) {
	newChain := func(t *testing.T) (*harness, string, string) {
		t.Helper()
		h := newHarness(t)
		_, owner := h.ownerSession(audience)
		childToken, _, err := h.authority.Child(context.Background(), owner, workcontext.ChildInput{
			Execution:   workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
			PrincipalID: agentID, PrincipalKind: "agent", AgentID: "fixture.test/agent:1.0.0",
			DelegationID: "d-1", Audience: audience, TTL: 30 * time.Minute,
			GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, []string{"codefly/core"})},
		})
		require.NoError(t, err)
		child := h.mustVerify(audience, childToken)
		grandToken, _, err := h.authority.Child(context.Background(), child, workcontext.ChildInput{
			Execution:   workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
			PrincipalID: "a-sub", PrincipalKind: "agent", AgentID: "fixture.test/sub:1.0.0",
			DelegationID: "d-2", Audience: audience, TTL: 10 * time.Minute,
			GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, []string{"codefly/core"})},
		})
		require.NoError(t, err)
		return h, childToken, grandToken
	}

	// Superseding the OWNER's build revokes the child and the grandchild.
	h, childToken, grandToken := newChain(t)
	require.NoError(t, h.seals.PutApprovedBuild(ownerID, workcontext.FixtureImageDigest, 12))
	for name, token := range map[string]string{"child": childToken, "grandchild": grandToken} {
		_, err := h.verify(audience, token)
		require.ErrorIsf(t, err, workcontext.ErrRevoked, "%s survived the owner's supersession", name)
		require.Contains(t, err.Error(), "the task owner")
	}

	// Superseding an INTERMEDIATE delegator's build revokes the grandchild.
	h, childToken, grandToken = newChain(t)
	require.NoError(t, h.seals.PutApprovedBuild(agentID, workcontext.FixtureImageDigest, 12))
	_, err := h.verify(audience, grandToken)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.Contains(t, err.Error(), "actor hop 0")
	_, err = h.verify(audience, childToken)
	require.ErrorIs(t, err, workcontext.ErrRevoked, "the child IS that hop, so it goes too")

	// Superseding the LAST hop's build revokes only what that hop exercises.
	h, childToken, grandToken = newChain(t)
	require.NoError(t, h.seals.PutApprovedBuild("a-sub", workcontext.FixtureImageDigest, 12))
	_, err = h.verify(audience, grandToken)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.NotNil(t, h.mustVerify(audience, childToken),
		"a later hop's supersession does not reach back up the chain")
}

// TestAnUnknownPrincipalIsARefusalAndBearingNoneIsRecorded is N3: "nothing
// recorded" was the most permissive answer the source gives.
//
// ApprovedBuild returned ErrNoApprovedBuild for ANY unknown principal, so a
// service principal whose build record was simply missing got a capability
// carrying no execution and every verifier accepted it — nothing can then
// revoke it by replacing its build. The kit's own delegated fixtures had an
// agent hop with an empty execution for exactly this reason.
//
// This is the FOURTH time this shape appeared here: an empty signer policy
// meaning "a renderer", an empty digest meaning "bears no execution", a zero
// applied record meaning "first generation", and this.
func TestAnUnknownPrincipalIsARefusalAndBearingNoneIsRecorded(t *testing.T) {
	source := workcontext.NewMemorySealSource()

	_, _, err := source.ApprovedBuild(context.Background(), "nobody-recorded-me")
	require.Error(t, err)
	require.NotErrorIs(t, err, workcontext.ErrNoApprovedBuild,
		"an unknown principal is an issuer that cannot say, not a principal that bears none")
	require.ErrorContains(t, err, "PutBearsNoExecution")

	require.NoError(t, source.PutBearsNoExecution("person-ada"))
	_, _, err = source.ApprovedBuild(context.Background(), "person-ada")
	require.ErrorIs(t, err, workcontext.ErrNoApprovedBuild)

	// A workload does not become a human: declaring it bears none would
	// re-admit every capability that carries none.
	require.NoError(t, source.PutApprovedBuild("svc", workcontext.FixtureImageDigest, 3))
	require.ErrorIs(t, source.PutBearsNoExecution("svc"), workcontext.ErrInvalid)
}

// The hard-coded fixture public key must stay equal to the key it names, or
// the refusal in Verify silently stops matching anything.
func TestTheFixturePublicKeyConstantMatchesTheKey(t *testing.T) {
	public, _ := workcontext.FixtureKeyPair()
	// Round-trips through the exported accessor, which is the only thing a
	// consumer can see, so a drift in either direction fails here.
	require.True(t, workcontext.IsFixtureKeyForTest(public),
		"the written-out public key no longer matches FixtureKeyPair's")
}
