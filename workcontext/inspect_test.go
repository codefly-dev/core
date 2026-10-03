package workcontext_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
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
		// The four a client consumer asked for, all refused by the SCHEMA.
		// Inspect runs protovalidate, so it reaches core's answer — which is
		// the whole point: a hand parser reached a different one.
		"zero-principal-epoch":       workcontext.ErrInvalid,
		"zero-installation-revision": workcontext.ErrInvalid,
		"zero-build-incarnation":     workcontext.ErrInvalid,
		"partial-operation-binding":  workcontext.ErrInvalid,
		// The execution PAIRING rule, which is a schema message rule rather
		// than a field rule — so Inspect reaches it exactly as it reaches the
		// field rules above.
		"seal-half-execution": workcontext.ErrInvalid,
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
				InstallationID: installation, InstallationRevision: 4,
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
		Execution:   workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
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
	// consumer pinning its own credential lifetime to MaxTTLCeiling — which a
	// now does, reading core's constant rather than carrying a number of its
	// own — must not find its own ceiling refused by one second of slack.
	claims.ExpiresAtUnix = time.Unix(claims.GetNotBeforeUnix(), 0).Add(workcontext.MaxTTLCeiling).Unix()
	inspected, err := workcontext.Inspect(h.resign(claims))
	require.NoError(t, err)
	require.Equal(t, workcontext.MaxTTLCeiling, inspected.ExpiresAt().Sub(inspected.NotBefore()))
}

// TestTheDecodePathHoldsItsOwnBoundsOnEveryEntrypoint covers W10 and W14,
// whose guards had no committed test — a review found both deletable with the
// suite green, which is the same as not having written them.
//
// W10: MaxTokenSize was once enforced in Inspect alone, so Verify — the strong
// path, the one a receiver actually uses — had no bound at all. It lives in
// decodeClaims, and all three entrypoints inherit it.
//
// W14: the payload must BE its own canonical encoding. Without it a second
// minter emitting a different-but-valid encoding of the same claims passes
// verification and the whole conformance kit, because the signature covers
// whatever bytes were presented. No fixture carries a non-canonical payload,
// so nothing held this.
func TestTheDecodePathHoldsItsOwnBoundsOnEveryEntrypoint(t *testing.T) {
	h := newHarness(t)
	_, verified := h.ownerSession(audience)

	oversized := strings.Repeat("A", workcontext.MaxTokenSize+1)
	for name, check := range map[string]func(string) error{
		"Inspect": func(token string) error {
			_, err := workcontext.Inspect(token)
			return err
		},
		"Verify": func(token string) error {
			_, err := h.verify(audience, token)
			return err
		},
		"Authenticate": func(token string) error {
			_, err := h.authenticator(audience).Authenticate(context.Background(), token)
			return err
		},
	} {
		t.Run("size/"+name, func(t *testing.T) {
			err := check(oversized)
			require.ErrorIs(t, err, workcontext.ErrInvalid)
			require.ErrorContains(t, err, "over the")
		})
	}

	// A NON-CANONICAL encoding of sound claims, signed with the real key: the
	// claims are identical, the signature is valid over the bytes presented,
	// and only byte-equality against a deterministic re-marshal catches it.
	//
	// THE FIRST VERSION OF THIS TEST DID NOT DISCRIMINATE, and a reviewer
	// struck it. It marshalled with Deterministic: false and fell back to
	// `canonical + 0x00` when that produced the same bytes — which it does.
	// A trailing zero byte is a field-0 tag, which proto.Unmarshal refuses
	// before the canonical check is ever reached, so the test passed on the
	// WRONG refusal and disabling the canonical comparison left the suite
	// green. Mutation-verified now, which is what should have been done then.
	//
	// The construction below is a real one: proto3 permits a scalar field to
	// appear twice on the wire, last occurrence winning. Repeating field 1
	// with the SAME value unmarshals to an identical message — so no other
	// check can object — while a deterministic re-marshal emits it once. Only
	// byte equality separates them.
	claims := verified.Context()
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(claims)
	require.NoError(t, err)
	require.Equal(t, byte(0x0a), canonical[0], "field 1, wire type 2: the Typ string leads the canonical encoding")
	field1 := canonical[:2+int(canonical[1])]
	noncanonical := append(append([]byte{}, canonical...), field1...)
	require.NotEqual(t, canonical, noncanonical)

	// It really is the same message: nothing but the byte comparison differs.
	roundTripped := &basev0.WorkContextV1{}
	require.NoError(t, proto.Unmarshal(noncanonical, roundTripped),
		"the encoding must be one Unmarshal ACCEPTS, or the test proves a different refusal")
	require.True(t, proto.Equal(claims, roundTripped), "the claims are identical")

	token := base64.RawURLEncoding.EncodeToString(noncanonical) + "." +
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.authority.Key, noncanonical))
	for name, check := range map[string]func(string) error{
		"Inspect": func(tk string) error {
			_, err := workcontext.Inspect(tk)
			return err
		},
		"Verify": func(tk string) error {
			_, err := h.verify(audience, tk)
			return err
		},
	} {
		err := check(token)
		require.ErrorIsf(t, err, workcontext.ErrInvalid, "%s accepted a non-canonical encoding", name)
		require.ErrorContainsf(t, err, "canonical", "%s refused it for the wrong reason", name)
	}
}

// TestRecheckRefusesACapabilityThatIsNotThisVerifiersToAnswer covers the gap a
// round-four review reproduced, plus A1 — Authenticator.Recheck's forwarding,
// whose guard had no test.
//
// Recheck's safety argument was that its argument cannot be obtained except by
// verifying, so it can never be a first verification. True, and incomplete: a
// *Verified obtained from one verifier was accepted by another, which never
// checked the capability was addressed to it. A gateway for one audience could
// report live a capability minted by another issuer for somebody else.
//
// Verify checks issuer and audience first; Recheck skipped them because they
// cannot CHANGE under a long-running call. That was the wrong test — the
// question is not what can change, but what this verifier may answer about.
func TestRecheckRefusesACapabilityThatIsNotThisVerifiersToAnswer(t *testing.T) {
	h := newHarness(t)
	_, verified := h.ownerSession(audience)

	require.NoError(t, h.verifier(audience).Recheck(context.Background(), verified))

	foreignIssuer := h.verifier(audience)
	foreignIssuer.Issuer = "https://someone-else.test"
	err := foreignIssuer.Recheck(context.Background(), verified)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "this verifier answers for")

	foreignAudience := h.verifier(audience)
	foreignAudience.Audience = "another-audience"
	err = foreignAudience.Recheck(context.Background(), verified)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "addressed to")

	// N6: a verifier that cannot VERIFY must not RECHECK either. These were
	// guarded with `v.Issuer != ""`, so a verifier with no issuer, no audience
	// and no keys — which Verify refuses outright — rechecked anything. That
	// guard was the fifth appearance of "the absent value means skip the
	// check" in this package, and this one was in my own fix for the issuer
	// gap two commits earlier.
	blind := h.verifier(audience)
	blind.Issuer, blind.Audience = "", ""
	blind.Keys = nil
	_, err = blind.Verify(context.Background(), verified.Encoded())
	require.Error(t, err, "Verify refuses it")
	require.Error(t, blind.Recheck(context.Background(), verified),
		"and so must Recheck, for the same inputs")

	noKeys := h.verifier(audience)
	noKeys.Keys = nil
	err = noKeys.Recheck(context.Background(), verified)
	require.ErrorContains(t, err, "key set",
		"a verifier holding no key cannot check a signature, so it cannot report on one")

	// A1: the Authenticator forwards to the same check rather than answering
	// nil, and inherits the refusal with it.
	token, _ := h.ownerSession(audience)
	authenticator := h.authenticator(audience)
	authenticated, err := authenticator.Authenticate(context.Background(), token)
	require.NoError(t, err)
	require.NoError(t, authenticator.Recheck(context.Background(), authenticated))

	foreign := h.authenticator(audience)
	foreign.Audience = "another-audience"
	require.ErrorIs(t, foreign.Recheck(context.Background(), authenticated), workcontext.ErrInvalid)
	require.ErrorIs(t, foreign.Recheck(context.Background(), nil), workcontext.ErrInvalid)
}
