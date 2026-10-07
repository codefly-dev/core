package workcontext_test

import (
	"context"
	"crypto/ed25519"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/workcontext"
)

// inspector is the harness's forwarding hop: the same trust root and clock
// its verifier uses, and none of the sources. The route target is given per
// call, as a hop gives it.
func (h *harness) inspector() *workcontext.Inspector {
	return &workcontext.Inspector{
		Issuer: issuer,
		Keys:   map[string]ed25519.PublicKey{keyID: h.public},
		Now:    func() time.Time { return h.clock },
	}
}

// THE property the hop's entrypoint rests on: for every fixture in the kit a
// hop can see, Inspect reaches the same decision as Verify AND reports it with
// the same message, byte for byte; and for every fixture refused only against
// the issuer's live state, Inspect accepts where Verify refuses — because a
// hop holds no such state and must not pretend to.
//
// Equal messages is the claim worth asserting, as it is for Authenticate: two
// bodies can agree on accept/refuse for every case anyone thought to test and
// still differ on which rule fired. Identical text is what one body produces,
// and it is what breaks the moment a second one appears.
func TestInspectAndVerifyAgreeOnEveryFixtureAHopCanSeeIncludingTheMessage(t *testing.T) {
	now := time.Now()
	fixtures, err := workcontext.Fixtures(now)
	require.NoError(t, err)
	require.NotEmpty(t, fixtures)

	clock := func() time.Time { return now }
	replay := workcontext.NewMemoryReplayStore()
	replay.Now = clock
	verifier := &workcontext.Verifier{
		TrustTheConformanceFixtureKey: true,
		Issuer:                        workcontext.FixtureIssuer,
		Audience:                      workcontext.FixtureAudience,
		Keys:                          workcontext.FixtureKeys(),
		Revisions:                     workcontext.FixtureRevisions(),
		Replay:                        replay,
		Grants:                        workcontext.FixtureGrants(now),
		Seals:                         workcontext.FixtureSeals(),
		Now:                           clock,
	}
	inspector := &workcontext.Inspector{
		TrustTheConformanceFixtureKey: true,
		Issuer:                        workcontext.FixtureIssuer,
		Keys:                          workcontext.FixtureKeys(),
		Now:                           clock,
	}

	var forwarded, agreed int
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			// Inspect FIRST, so a consuming inspector would be caught by the
			// verifier's refusal of the single-use fixture right after it.
			_, inspectErr := inspector.Inspect(fixture.Token, workcontext.FixtureAudience)
			_, verifyErr := verifier.Verify(context.Background(), fixture.Token)

			if fixture.NeedsLiveState {
				forwarded++
				require.ErrorIs(t, verifyErr, workcontext.ErrRevoked, "the callee holds the state, so it refuses")
				require.NoError(t, inspectErr, "fixture %q is refused only against live state a hop does not hold; "+
					"an inspector refusing it is claiming a check it cannot make", fixture.Name)
				return
			}
			agreed++
			if verifyErr == nil {
				require.NoError(t, inspectErr, "fixture %q verifies; the hop must forward it", fixture.Name)
				return
			}
			require.Error(t, inspectErr)
			require.Equal(t, verifyErr.Error(), inspectErr.Error(),
				"fixture %q must be refused for the SAME named reason by the hop and the callee", fixture.Name)
		})
	}
	require.NotZero(t, forwarded, "the kit must mark at least one fixture as needing live state")
	require.NotZero(t, agreed, "the kit must hold most fixtures to identical outcomes")
}

// Inspect consumes nothing. A single-use grant capability inspects as many
// times as it is presented, and the callee's Verify afterwards finds the nonce
// intact — accepting once, and refusing the second time, so the first
// acceptance meant something.
//
// This is the finding the entrypoint exists for: a gateway that verified
// burned the nonce in flight and the callee refused a legitimate capability as
// replayed.
func TestInspect_ConsumesNothing(t *testing.T) {
	h := newHarness(t)
	_, token, _ := h.elevated(t)

	for presentation := 0; presentation < 5; presentation++ {
		inspected, err := h.inspector().Inspect(token, mergeTool)
		require.NoError(t, err, "presentation %d: a hop inspects every time", presentation)
		require.Equal(t, mergeTool, inspected.Audience())
	}
	_, err := h.verify(mergeTool, token)
	require.NoError(t, err, "the callee's verification finds the nonce intact after five inspections")
	_, err = h.verify(mergeTool, token)
	require.ErrorIs(t, err, workcontext.ErrReplayed, "and the callee consumed it, so the acceptance before was real")

	// After the callee consumed it, the hop still inspects it: a hop cannot
	// tell a spent capability from a live one, and must not pretend to.
	_, err = h.inspector().Inspect(token, mergeTool)
	require.NoError(t, err)
}

// What only the issuer can refuse, the hop forwards. Every revocation lever —
// the authorization revision, the owner's epoch, the actor's epoch, the
// installation revision — moves Verify to ErrRevoked and moves Inspect not at
// all.
func TestInspect_ForwardsWhatOnlyTheIssuerCanRefuse(t *testing.T) {
	for name, move := range map[string]func(*harness){
		"the authorization revision": func(h *harness) { h.revision++ },
		"the installation revision": func(h *harness) {
			require.NoError(h.t, h.seals.Put(ownerID, workcontext.Seal{
				InstallationID: installation, InstallationRevision: 4,
			}))
		},
		"the owner's epoch": func(h *harness) { require.NoError(h.t, h.seals.PutEpoch(ownerID, 3)) },
		"the actor's epoch": func(h *harness) { require.NoError(h.t, h.seals.PutEpoch(agentID, 2)) },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			_, owner := h.ownerSession(audience)
			token, _ := h.agentSession(owner, audience)

			move(h)
			_, err := h.verify(audience, token)
			require.ErrorIs(t, err, workcontext.ErrRevoked, "the callee refuses once %s moves", name)
			inspected, err := h.inspector().Inspect(token, audience)
			require.NoError(t, err, "the hop cannot see %s move, and must not claim to", name)
			require.Equal(t, tenant, inspected.TenantID())
		})
	}
}

// A grant capability is forwarded, not judged: the callee holds the approval
// records. The contrast with Authenticator is the point — an Authenticator
// ACTS on what it accepts, so it refuses with ErrNeedsIssuer; a hop forwards,
// so for a hop the deferral IS the forwarding.
func TestInspect_ForwardsAGrantCapabilityForTheCalleeToJudge(t *testing.T) {
	h := newHarness(t)
	_, token, _ := h.elevated(t)

	_, err := h.authenticator(mergeTool).Authenticate(context.Background(), token)
	require.ErrorIs(t, err, workcontext.ErrNeedsIssuer, "a consumer without the records defers")

	inspected, err := h.inspector().Inspect(token, mergeTool)
	require.NoError(t, err, "a hop forwards to the party that holds the records")
	require.Equal(t, taskID, inspected.TaskID())
}

// The route target is the hop's. A capability addressed elsewhere is refused
// with the audience refusal, and the same capability inspects for the route it
// names — so the refusal is the route and not the token.
func TestInspect_RefusesACapabilityAddressedToAnotherRoute(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	_, err := h.inspector().Inspect(token, "codefly.dev/other-bot:0.1.0")
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "presented to")

	inspected, err := h.inspector().Inspect(token, audience)
	require.NoError(t, err)
	require.Equal(t, audience, inspected.Audience())
}

// An empty route target is refused BY NAME, before anything is decoded. With
// this check deleted the comparison below it still refuses — a schema-valid
// capability never names an empty audience — but with "presented to", which
// reads as a mismatch rather than as the hop having asked about nothing. The
// message is the witness.
func TestInspect_RefusesAnEmptyRouteTarget(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	_, err := h.inspector().Inspect(token, "")
	require.Error(t, err)
	require.ErrorContains(t, err, "route target")
	require.NotContains(t, err.Error(), "presented to", "an empty expectation is refused as such, not as a mismatch")
}

// A capability addressed to NO audience is refused by the schema, so the hop
// and the callee refuse it with one message, and an empty expectation at a hop
// can never be matched by one.
func TestInspect_RefusesACapabilityAddressedToNoAudience(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	claims := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	claims.Audience = ""
	token := h.resign(claims)

	_, inspectErr := h.inspector().Inspect(token, audience)
	require.ErrorIs(t, inspectErr, workcontext.ErrInvalid)
	require.ErrorContains(t, inspectErr, "audience")
	_, verifyErr := h.verify(audience, token)
	require.Equal(t, verifyErr.Error(), inspectErr.Error(), "one decode path, one message")
}

// The signature, under the hop's trust root. A forgery under a key the hop
// does not hold, a tampered payload under the real signature, and a key id the
// hop does not hold are each refused — with Verify's own messages.
func TestInspect_RefusesAForgery(t *testing.T) {
	h := newHarness(t)
	token, owner := h.ownerSession(audience)

	_, err := h.inspector().Inspect(h.resignWithAnotherKey(t, token), audience)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "signature does not verify")

	tampered := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	tampered.TenantId = "t-someone-else"
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(tampered)
	require.NoError(t, err)
	_, signature, _ := strings.Cut(token, ".")
	_, err = h.inspector().Inspect(encodePayload(payload)+"."+signature, audience)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "signature does not verify")

	rotatedOut := h.inspector()
	rotatedOut.Keys = map[string]ed25519.PublicKey{"k-2": h.public}
	_, err = rotatedOut.Inspect(token, audience)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "no verification key")

	// A malformed key is a refusal, not a panic: the key id is chosen by the
	// untrusted capability.
	short := h.inspector()
	short.Keys = map[string]ed25519.PublicKey{keyID: h.public[:16]}
	_, err = short.Inspect(token, audience)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "is 16 bytes")
}

// The issuer pin: a capability signed by a key the hop holds and naming
// another issuer is refused. The key is not the trust decision.
func TestInspect_RefusesAnotherIssuer(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	foreign := h.inspector()
	foreign.Issuer = "https://someone-else.test"
	_, err := foreign.Inspect(token, audience)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "issued by")
}

// The window, both ends, with the skew a Verifier applies.
func TestInspect_RefusesOutsideTheWindow(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	h.clock = h.clock.Add(2 * time.Hour)
	_, err := h.inspector().Inspect(token, audience)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "expired at")

	h.clock = h.clock.Add(-3 * time.Hour)
	_, err = h.inspector().Inspect(token, audience)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "not valid before")

	// Exactly as far past expiry as the skew allows is still accepted, and
	// one second past it is not — the same tolerance the callee applies.
	h.clock = h.clock.Add(3 * time.Hour).Add(-time.Hour).Add(workcontext.DefaultSkew)
	_, err = h.inspector().Inspect(token, audience)
	require.ErrorIs(t, err, workcontext.ErrInvalid, "the window closes at expiry plus skew, inclusive of the instant")
	h.clock = h.clock.Add(-time.Second)
	_, err = h.inspector().Inspect(token, audience)
	require.NoError(t, err)
}

// The seal's SHAPE is checked: no seal, a seal naming no installation, an actor
// hop with no epoch. A hop refuses what no verifier would accept, before it is
// forwarded, with the schema's own refusal.
func TestInspect_ChecksTheSealShape(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)

	unsealed := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	unsealed.Seal = nil
	_, err := h.inspector().Inspect(h.resign(unsealed), audience)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "seal")

	noInstallation := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	noInstallation.Seal.InstallationId = ""
	_, err = h.inspector().Inspect(h.resign(noInstallation), audience)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "installation_id")

	noEpoch := proto.Clone(agent.Context()).(*basev0.WorkContextV1)
	noEpoch.ActorChain[0].PrincipalEpoch = 0
	_, err = h.inspector().Inspect(h.resign(noEpoch), audience)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "principal_epoch")

	// And a widening hop, which the schema cannot state and checkStructure
	// does: a hop refuses it on the same body the callee uses.
	widened := proto.Clone(agent.Context()).(*basev0.WorkContextV1)
	widened.ActorChain[0].GrantedScopes = []*basev0.WorkScopeV1{scope("repo", []string{"read", "write", "admin"}, nil)}
	_, err = h.inspector().Inspect(h.resign(widened), audience)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "widens authority")
}

// The conformance fixture key is refused by a hop exactly as by a verifier,
// unless the hop says in a line somebody wrote that it is a conformance run.
func TestInspect_RefusesTheConformanceFixtureKeyUnlessTold(t *testing.T) {
	now := time.Now()
	fixtures, err := workcontext.Fixtures(now)
	require.NoError(t, err)
	index := slices.IndexFunc(fixtures, func(f workcontext.Fixture) bool { return f.Name == "session" })
	require.NotEqual(t, -1, index)
	session := fixtures[index].Token

	hop := &workcontext.Inspector{
		Issuer: workcontext.FixtureIssuer,
		Keys:   workcontext.FixtureKeys(),
		Now:    func() time.Time { return now },
	}
	_, err = hop.Inspect(session, workcontext.FixtureAudience)
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "conformance fixture key")

	hop.TrustTheConformanceFixtureKey = true
	_, err = hop.Inspect(session, workcontext.FixtureAudience)
	require.NoError(t, err)
}

// A hop with no trust root refuses everything, by name.
func TestInspect_RefusesWithoutATrustRoot(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	noIssuer := h.inspector()
	noIssuer.Issuer = ""
	_, err := noIssuer.Inspect(token, audience)
	require.ErrorContains(t, err, "names no issuer")

	noKeys := h.inspector()
	noKeys.Keys = nil
	_, err = noKeys.Inspect(token, audience)
	require.ErrorContains(t, err, "holds no verification key")
}

// What a hop routes on, read back from the capability it was minted with.
func TestInspect_ReturnsTheRoutingClaims(t *testing.T) {
	h := newHarness(t)
	token, owner := h.ownerSession(audience)

	inspected, err := h.inspector().Inspect(token, audience)
	require.NoError(t, err)
	require.Equal(t, audience, inspected.Audience())
	require.Equal(t, tenant, inspected.TenantID())
	require.Equal(t, installation, inspected.InstallationID())
	require.Equal(t, taskID, inspected.TaskID())
	require.Equal(t, owner.Context().GetSessionId(), inspected.SessionID())

	// A zero value answers nothing rather than panicking, like every other
	// result type here: an outside-constructed &Inspected{} is not a routing
	// decision, and a nil one is not either.
	var none *workcontext.Inspected
	require.Empty(t, none.Audience())
	require.Empty(t, (&workcontext.Inspected{}).TenantID())
}

// The shape of the two types IS the contract, held by reflection so a field or
// a method added later is a visible change here rather than a quiet widening:
//
//   - Inspector holds no source. There is no field through which it could
//     consume a nonce, read a revision, resolve a grant or compare a seal.
//   - Inspected exposes exactly the claims a hop routes on, every one a bare
//     string: no claims message, no scope, no actor, no grant hop, no binding,
//     no nonce, no token — nothing a callee acts on.
func TestInspectorHoldsNoSourceAndInspectedCarriesNothingACalleeActsOn(t *testing.T) {
	inspector := reflect.TypeOf(workcontext.Inspector{})
	var fields []string
	for index := 0; index < inspector.NumField(); index++ {
		field := inspector.Field(index)
		fields = append(fields, field.Name+" "+field.Type.String())
		for _, source := range []reflect.Type{
			reflect.TypeOf((*workcontext.ReplayStore)(nil)).Elem(),
			reflect.TypeOf((*workcontext.RevisionSource)(nil)).Elem(),
			reflect.TypeOf((*workcontext.GrantSource)(nil)).Elem(),
			reflect.TypeOf((*workcontext.SealSource)(nil)).Elem(),
		} {
			require.False(t, field.Type.Implements(source) || field.Type == source,
				"Inspector.%s is a %s; a hop holds no source", field.Name, source)
		}
	}
	require.Equal(t, []string{
		"Issuer string",
		"Keys map[string]ed25519.PublicKey",
		"Now func() time.Time",
		"Skew time.Duration",
		"TrustTheConformanceFixtureKey bool",
	}, fields, "Inspector is a trust root, a clock and the fixture-key flag, and nothing else")

	inspected := reflect.TypeOf(&workcontext.Inspected{})
	var methods []string
	for index := 0; index < inspected.NumMethod(); index++ {
		method := inspected.Method(index)
		require.Equal(t, 1, method.Type.NumOut(), "%s returns one value", method.Name)
		require.Equal(t, reflect.String, method.Type.Out(0).Kind(),
			"%s returns a bare string: nothing a callee could act on travels through an Inspected", method.Name)
		methods = append(methods, method.Name)
	}
	require.Equal(t, []string{"Audience", "InstallationID", "SessionID", "TaskID", "TenantID"}, methods,
		"Inspected exposes exactly the claims a hop routes on")
	require.Zero(t, reflect.TypeOf(workcontext.Inspected{}).NumField()-5, "and holds exactly those five")
	for index := 0; index < reflect.TypeOf(workcontext.Inspected{}).NumField(); index++ {
		require.False(t, reflect.TypeOf(workcontext.Inspected{}).Field(index).IsExported(),
			"an Inspected is read through its methods and built only by Inspect")
	}
}
