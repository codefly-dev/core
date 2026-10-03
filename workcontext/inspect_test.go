package workcontext_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
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
// answered its own sentinel where core answers ErrInvalid, and it never read
// the actor epochs at all. So the contract asserted here is agreement with the
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
		"missing-seal":              workcontext.ErrInvalid,
		"seal-without-installation": workcontext.ErrInvalid,
		"actor-without-epoch":       workcontext.ErrInvalid,
		// The four the sdk-go consumer asked for, all refused by the SCHEMA.
		// Inspect runs protovalidate, so it reaches core's answer — which is
		// the whole point: a hand parser reached a different one.
		"zero-principal-epoch":       workcontext.ErrInvalid,
		"zero-installation-revision": workcontext.ErrInvalid,
		"zero-build-incarnation":     workcontext.ErrInvalid,
		"partial-operation-binding":  workcontext.ErrInvalid,
		// Structural too, and deliberately so: a consumer's early refusal
		// catches a token some other minter wrote, before it goes on a request.
		"unknown-field": workcontext.ErrInvalid,
	}
	seen := map[string]bool{}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			inspected, err := workcontext.Inspect(fixture.Token)
			want, isStructural := structural[fixture.Name]
			if !isStructural {
				require.NoError(t, err,
					"fixture %q is refused for a reason Inspect cannot see, so Inspect must PASS it — "+
						"a structural check that refused it would be claiming to have verified something",
					fixture.Name)
				require.NotNil(t, inspected)
				require.NotEmpty(t, inspected.Context().GetOwnerPrincipalId(),
					"a holder reads its own credential through this, so the claims must be there")
				return
			}
			seen[fixture.Name] = true
			require.Nil(t, inspected, "a refusal yields nothing to read")
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

	sound, err := workcontext.Inspect(token)
	require.NoError(t, err)
	require.NotNil(t, sound)

	// Re-signed by a key the verifier does not hold: structurally identical.
	forged := h.resignWithAnotherKey(t, token)
	inspected, err := workcontext.Inspect(forged)
	require.NoError(t, err,
		"Inspect checks no signature, so a forgery passes it — which is why Inspected is not Verified and cannot become it")
	require.NotNil(t, inspected)
	_, err = h.verify(audience, forged)
	require.Error(t, err, "and verification refuses it")
}

// Inspect catches the case the ask named: an unsealed capability, refused
// before it is put on a request rather than after the receiver rejects it.
func TestInspectRefusesAnUnsealedCapabilityBeforeItIsSent(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	unsealed := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	unsealed.Seal = nil
	_, err := workcontext.Inspect(h.resign(unsealed))
	require.ErrorIs(t, err, workcontext.ErrInvalid)

	noEpoch := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	noEpoch.ActorChain = []*basev0.WorkActorV1{{
		PrincipalId: agentID, PrincipalKind: "agent",
		AgentId: proto.String("fixture.test/agent:1.0.0"), DelegationId: "d-1",
	}}
	_, err = workcontext.Inspect(h.resign(noEpoch))
	require.ErrorIs(t, err, workcontext.ErrInvalid)
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
				ImageDigest: workcontext.FixtureImageDigest, InstallationID: installation, InstallationRevision: 4, BuildIncarnation: 11,
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

// A holder reads its own credential through Inspect: the expiry it needs to
// know when to mint afresh, and the seal it is bound to. This is the use case
// that made error-only wrong — without it a holder keeps a hand parser.
func TestInspect_GivesAHolderWhatItNeedsToReadItsOwnCredential(t *testing.T) {
	h := newHarness(t)
	token, owner := h.ownerSession(audience)

	inspected, err := workcontext.Inspect(token)
	require.NoError(t, err)
	require.Equal(t, owner.Context().GetExpiresAtUnix(), inspected.ExpiresAt().Unix())
	require.Equal(t, owner.Context().GetNotBeforeUnix(), inspected.NotBefore().Unix())
	require.Equal(t, installation, inspected.Seal().GetInstallationId())
	require.Equal(t, uint64(3), inspected.Seal().GetInstallationRevision())
	require.Equal(t, ownerID, inspected.Context().GetOwnerPrincipalId())
}

// The size bound is core's, not each consumer's. A consumer had invented a
// 32KiB bound because core declared none.
func TestInspect_BoundsTheTokenBeforeDecodingIt(t *testing.T) {
	_, err := workcontext.Inspect(strings.Repeat("A", workcontext.MaxTokenSize+1))
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "over the")
}

// An Inspected capability cannot become a Verified one, and nothing in the
// package offers a way to make it one. Same guard as Authenticated's, for the
// same reason: that conversion would be the whole downgrade in one function.
func TestNoDeclarationTurnsAnInspectedCapabilityIntoAVerifiedOne(t *testing.T) {
	set := token.NewFileSet()
	packages, err := parser.ParseDir(set, ".", func(file os.FileInfo) bool {
		return !strings.HasSuffix(file.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	for _, file := range packages["workcontext"].Files {
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || !function.Name.IsExported() {
				continue
			}
			var inputs []string
			for _, fields := range []*ast.FieldList{function.Recv, function.Type.Params} {
				if fields == nil {
					continue
				}
				for _, field := range fields.List {
					inputs = append(inputs, typeName(field.Type))
				}
			}
			if !slices.Contains(inputs, "*Inspected") && !slices.Contains(inputs, "Inspected") {
				continue
			}
			if function.Type.Results == nil {
				continue
			}
			for _, result := range function.Type.Results.List {
				require.NotContains(t, []string{"*Verified", "Verified", "*Authenticated", "Authenticated"},
					typeName(result.Type),
					"%s at %s converts an Inspected capability into a trust-bearing one",
					function.Name.Name, set.Position(function.Pos()))
			}
		}
	}
}

// The minter has a ceiling. Without one a misconfigured host mints a month-long
// credential and every verifier accepts it for a month.
func TestStart_RefusesATTLBeyondTheCeiling(t *testing.T) {
	h := newHarness(t)
	_, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		Execution:      workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		InstallationID: installation, TenantID: tenant, OwnerPrincipalID: ownerID,
		OwnerPrincipalKind: "human", TaskID: taskID, Audience: audience,
		TTL: 30 * 24 * time.Hour,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "no authority mints beyond")

	// And the ABSOLUTE bound is not raisable: MaxTTL above it is clamped, not
	// honoured. A consumer measured that MaxTTL took any positive value and
	// Verify bounded no lifetime, so between a host setting thirty days and a
	// process holding the credential there was nothing — and declined my
	// advice to drop its own ceiling, correctly.
	h.authority.MaxTTL = 30 * 24 * time.Hour
	_, _, err = h.authority.Start(context.Background(), workcontext.StartInput{
		Execution:      workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		InstallationID: installation, TenantID: tenant, OwnerPrincipalID: ownerID,
		OwnerPrincipalKind: "human", TaskID: taskID, Audience: audience,
		TTL: 48 * time.Hour,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "no authority mints beyond")

	// A host with a longer legitimate need states it, within the bound.
	h.authority.MaxTTL = 12 * time.Hour
	_, _, err = h.authority.Start(context.Background(), workcontext.StartInput{
		Execution:      workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		InstallationID: installation, TenantID: tenant, OwnerPrincipalID: ownerID,
		OwnerPrincipalKind: "human", TaskID: taskID, Audience: audience,
		TTL: 8 * time.Hour,
	})
	require.NoError(t, err)
}

// And the ceiling applies to a delegated hop too, so a child cannot outlive it.
func TestChild_RefusesATTLBeyondTheCeiling(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, _, err := h.authority.Child(context.Background(), owner, workcontext.ChildInput{
		PrincipalID: agentID, PrincipalKind: "agent", AgentID: "fixture.test/agent:1.0.0",
		DelegationID:  "d-1",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:      audience, TTL: 30 * 24 * time.Hour,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "no authority mints beyond")
}

// TestTheLifetimeBoundIsOneBoundOnEveryEntrypoint holds the bound where it
// belongs rather than where it was first written.
//
// The bound arrived in Verify alone. Verify is the strong path, so the hole
// looked harmless — but the caller that reads a window WITHOUT verifying is a
// mint client, which holds a credential minted for it and asks Inspect when
// it expires. It never checks a signature, because it is not a receiver. So a
// capability no receiver on any current Core would accept was reported to its
// holder as valid for thirty days: not an absent answer, a false one, about
// the single field Inspect is called for.
//
// The bound therefore lives in decodeClaims, the one decode path, which is
// where this file's own comment said a bound belongs before this one was put
// somewhere else. All three entrypoints inherit it and the message is one
// message, so a consumer that matches on it matches whichever path it took.
func TestTheLifetimeBoundIsOneBoundOnEveryEntrypoint(t *testing.T) {
	h := newHarness(t)
	_, verified := h.ownerSession(audience)
	claims := verified.Context()
	// Thirty days: no Authority will mint it, so it is resigned directly.
	claims.ExpiresAtUnix = time.Unix(claims.GetNotBeforeUnix(), 0).Add(30 * 24 * time.Hour).Unix()
	token := h.resign(claims)

	_, err := workcontext.Inspect(token)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "720h0m0s")
	require.Contains(t, err.Error(), "beyond 24h0m0s")

	_, err = h.verifier(audience).Verify(t.Context(), token)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "beyond 24h0m0s")

	_, err = h.authenticator(audience).Authenticate(t.Context(), token)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.Contains(t, err.Error(), "beyond 24h0m0s")

	// Exactly at the ceiling is accepted: the bound is a maximum, and a
	// consumer pinning MaxCredentialLifetime to MaxTTLCeiling — which sdk-go
	// now does, reading core's constant rather than carrying a number of its
	// own — must not find its own ceiling refused by one second of slack.
	claims.ExpiresAtUnix = time.Unix(claims.GetNotBeforeUnix(), 0).Add(workcontext.MaxTTLCeiling).Unix()
	inspected, err := workcontext.Inspect(h.resign(claims))
	require.NoError(t, err)
	require.Equal(t, workcontext.MaxTTLCeiling, inspected.ExpiresAt().Sub(inspected.NotBefore()))
}
