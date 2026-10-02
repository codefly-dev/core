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

// Inspect and Verify must not disagree about what a token structurally IS.
//
// This is the finding it exists for: a consumer hand-parsing a carried
// capability reached different sentinels than core's fixtures declare — one
// answered ErrUnsealed where core answers ErrInvalid, and it never read the
// actor epochs at all. So the contract asserted here is agreement with the
// kit, fixture by fixture, for every refusal that is structural.
func TestInspectAgreesWithTheKitOnEveryStructuralRefusal(t *testing.T) {
	now := time.Now()
	fixtures, err := workcontext.Fixtures(now)
	require.NoError(t, err)

	// The refusals that are structural — reachable without any issuer state.
	// Everything else in the kit is refused for a reason Inspect deliberately
	// cannot see, and is asserted to PASS Inspect below.
	structural := map[string]error{
		"foreign-encoding":          workcontext.ErrNotACoreToken,
		"no-separator":              workcontext.ErrInvalid,
		"payload-not-b64":           workcontext.ErrInvalid,
		"empty-token":               workcontext.ErrInvalid,
		"separator-only":            workcontext.ErrInvalid,
		"missing-seal":              workcontext.ErrUnsealed,
		"seal-without-installation": workcontext.ErrInvalid,
		"actor-without-epoch":       workcontext.ErrUnsealed,
	}
	seen := map[string]bool{}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			err := workcontext.Inspect(fixture.Token)
			want, isStructural := structural[fixture.Name]
			if !isStructural {
				require.NoError(t, err,
					"fixture %q is refused for a reason Inspect cannot see, so Inspect must PASS it — "+
						"a structural check that refused it would be claiming to have verified something",
					fixture.Name)
				return
			}
			seen[fixture.Name] = true
			require.ErrorIs(t, err, want, "Inspect must reach the kit's own sentinel")
		})
	}
	require.Len(t, seen, len(structural), "every structural case must have been exercised")
}

// Inspect authenticates nothing: a token signed by a key nobody holds, and a
// token whose payload was tampered with, both pass it.
//
// Asserted rather than only documented, because this is the misreading that
// would matter. A caller treating nil as permission has skipped verification,
// and the way to make that undeniable is to show that Inspect says yes to a
// forgery.
func TestInspectAuthenticatesNothing(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	require.NoError(t, workcontext.Inspect(token))

	// Re-signed by a key the verifier does not hold: structurally identical.
	forged := h.resignWithAnotherKey(t, token)
	require.NoError(t, workcontext.Inspect(forged),
		"Inspect checks no signature, so a forgery passes it — this is why it returns no claims")
	_, err := h.verify(audience, forged)
	require.Error(t, err, "and verification refuses it")
}

// Inspect catches the case the ask named: an unsealed capability, refused
// before it is put on a request rather than after the receiver rejects it.
func TestInspectRefusesAnUnsealedCapabilityBeforeItIsSent(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	unsealed := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	unsealed.Seal = nil
	require.ErrorIs(t, workcontext.Inspect(h.resign(unsealed)), workcontext.ErrUnsealed)

	noEpoch := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	noEpoch.ActorChain = []*basev0.WorkActorV1{{
		PrincipalId: agentID, PrincipalKind: "agent",
		AgentId: proto.String("codefly.dev/mind:1.2.0"), DelegationId: "d-1",
	}}
	require.ErrorIs(t, workcontext.Inspect(h.resign(noEpoch)), workcontext.ErrUnsealed)
}

// Recheck re-reads live state without consuming the nonce, which is the whole
// reason it exists: Verify consumes a single-use capability, so a stream guard
// that re-verified before each emission killed the stream on its first check.
func TestRecheck_DoesNotConsumeASingleUseCapability(t *testing.T) {
	h := newHarness(t)
	agent, token, _ := h.elevated(t)
	_ = agent
	verified := h.mustVerify(mergeTool, token)

	verifier := h.verifier(mergeTool)
	for i := 0; i < 5; i++ {
		require.NoError(t, verifier.Recheck(context.Background(), verified),
			"re-check %d: liveness and consumption are different operations", i)
	}
	// And the capability is still spent exactly once overall: presenting it
	// again to Verify is still a replay.
	_, err := h.verify(mergeTool, token)
	require.ErrorIs(t, err, workcontext.ErrReplayed)
}

// Every lever that can move under a running stream cuts it.
func TestRecheck_RefusesEveryLeverThatCanMove(t *testing.T) {
	for name, move := range map[string]func(*harness){
		"the authorization revision": func(h *harness) { h.revision++ },
		"the installation revision": func(h *harness) {
			require.NoError(h.t, h.seals.Put(ownerID, workcontext.Seal{
				InstallationID: installation, InstallationRevision: 4, BuildIncarnation: 11,
			}))
		},
		"the owner's epoch": func(h *harness) { require.NoError(h.t, h.seals.PutEpoch(ownerID, 3)) },
		"the actor's epoch": func(h *harness) { require.NoError(h.t, h.seals.PutEpoch(agentID, 2)) },
		"the window":        func(h *harness) { h.clock = h.clock.Add(2 * time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			_, owner := h.ownerSession(audience)
			_, agent := h.agentSession(owner, audience)
			verifier := h.verifier(audience)
			require.NoError(t, verifier.Recheck(context.Background(), agent))

			move(h)
			require.Error(t, verifier.Recheck(context.Background(), agent),
				"a stream holding this capability must be cut when %s moves", name)
		})
	}
}

// Recheck needs its sources, like every other live check.
func TestRecheck_RefusesWithoutItsSources(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	verifier := h.verifier(audience)
	verifier.Seals = nil
	require.Error(t, verifier.Recheck(context.Background(), owner))
	require.Error(t, (&workcontext.Verifier{}).Recheck(context.Background(), nil))
}
