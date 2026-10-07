package solutionhost_test

import (
	"encoding/json"
	"testing"

	"github.com/codefly-dev/core/composition"
	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

// The canonical encoding of the shipped fixtures, pinned.
//
// Applied.Digest is host-durable state: a host stores it with the generation it
// applied and compares a freshly computed digest against it on every reconcile
// pass. So the encoding is a compatibility surface, and anything that moves it
// — a field reordered for readability, a renamed tag, a field added without
// omitempty — silently invalidates every stored digest and makes each host read
// its own Core upgrade as ErrRewrittenGeneration, an error that accuses
// delivery of tampering. Before these constants existed, reordering two struct
// fields moved the "valid" digest and the whole suite stayed green.
//
// Changing any constant is therefore a deliberate act with a migration behind
// it, never a side effect of an edit. The presence values have moved twice,
// both times with a schema step — v1 → v2, then v2 → v3 for an endpoint's
// exposure: the document itself changed, so every stored digest is stale by
// construction and a host treats it as stale rather than as evidence of a
// rewrite. That is the migration, and a schema step is the only reason these
// may move.
const (
	validFixtureDigest     = "sha256:4a8ed1bea464770db4d63a6fb74fe5cafc56b60e9463a4df4819222e7ca230e7"
	tombstoneFixtureDigest = "sha256:e095c1d5ded6f71c9c53e2650c1d269d126b9209da0a57969c9155549c2f719e"
)

func TestCanonicalEncodingIsPinnedAgainstTheShippedFixtures(t *testing.T) {
	for name, expected := range map[string]string{
		"valid": validFixtureDigest, "tombstone": tombstoneFixtureDigest,
	} {
		t.Run(name, func(t *testing.T) {
			digest, err := parse(t, name).Digest()
			require.NoError(t, err)
			require.Equal(t, expected, digest,
				"the canonical encoding moved; every host's stored Applied.Digest is now stale")
		})
	}
}

// Keys are emitted in name order at every depth, which is what makes the digest
// independent of this struct's Go declaration order.
func TestCanonicalEncodingSortsKeysAtEveryDepth(t *testing.T) {
	canonical, err := parse(t, "valid").CanonicalBytes()
	require.NoError(t, err)
	require.Contains(t, string(canonical), `{"artifacts":[{"digest":`)
	require.Contains(t, string(canonical), `"host":{"component":"solution-host","coordinate":`)
	// A number survives as its literal rather than as a rounded float.
	require.Contains(t, string(canonical), `"generation":4`)
}

// A generation past float64's exact range must still digest to its own value.
// Round-tripping the canonical form through a generic value would otherwise
// round two neighbouring generations onto one digest.
func TestALargeGenerationKeepsItsExactValue(t *testing.T) {
	low := parse(t, "valid")
	low.Generation = 1 << 60
	high := parse(t, "valid")
	high.Generation = 1<<60 + 1

	lowDigest, err := low.Digest()
	require.NoError(t, err)
	highDigest, err := high.Digest()
	require.NoError(t, err)
	require.NotEqual(t, lowDigest, highDigest)

	canonical, err := high.CanonicalBytes()
	require.NoError(t, err)
	require.Contains(t, string(canonical), `"generation":1152921504606846977`)
}

// A release publisher and name are one segment each. Identity() joins them with
// "/" and "@", so a "/" inside either would let publisher "example" + name
// "alpha/web" and publisher "example/alpha" + name "web" render the same identity —
// and every artifact references its release by exactly that string.
func TestReleaseIdentityCannotBeAmbiguous(t *testing.T) {
	for name, release := range map[string]solutionhost.Release{
		"slash in publisher": {Publisher: "example/alpha", Name: "web", Version: "1.0.0"},
		"slash in name":      {Publisher: "example", Name: "alpha/web", Version: "1.0.0"},
	} {
		t.Run(name, func(t *testing.T) {
			document := parse(t, "valid")
			document.Release = release
			for index := range document.Artifacts {
				document.Artifacts[index].Release = release.Identity()
			}
			err := document.Validate()
			require.ErrorIs(t, err, solutionhost.ErrInvalid)
			require.Contains(t, err.Error(), "one segment")
		})
	}

	// The two pairs that used to collide now cannot both exist.
	require.NotEqual(t,
		solutionhost.Release{Publisher: "example", Name: "alpha", Version: "1.0.0"}.Identity(),
		solutionhost.Release{Publisher: "example", Name: "alpha-web", Version: "1.0.0"}.Identity())
}

// Route aliases are unique within ONE host. A product delivery repo covers
// every host it deploys to, so the renderer's set legitimately carries the same
// alias on two coordinates — that is one solution in two regions, not a
// collision.
func TestTheSameAliasOnTwoCoordinatesIsNotACollision(t *testing.T) {
	eu := parse(t, "valid")
	us := parse(t, "valid")
	us.Binding = "alpha-region-b-01"
	us.Host.Coordinate = "example/prod/region-b"
	require.Equal(t, eu.Aliases(), us.Aliases())

	admissions, err := solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: eu, FirstRecord: true}, solutionhost.RenderedSet{Document: us, FirstRecord: true})
	require.NoError(t, err)
	// RenderedAdmission, not Admission: AdmitRendered answering a host's own
	// type was C4's fail-open under another name. Fold is the generation
	// decision, DecisionApply here because no record was supplied.
	require.Equal(t, []solutionhost.RenderedAdmission{
		{Binding: eu.Binding, Decision: solutionhost.DecisionApply, Fold: solutionhost.DecisionApply},
		{Binding: us.Binding, Decision: solutionhost.DecisionApply, Fold: solutionhost.DecisionApply},
	}, admissions)

	// The same alias twice on ONE coordinate still collides.
	us.Host.Coordinate = eu.Host.Coordinate
	_, err = solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: eu, FirstRecord: true}, solutionhost.RenderedSet{Document: us, FirstRecord: true})
	require.ErrorIs(t, err, composition.ErrCollision)
}

// Applied state is one host's durable record and names no coordinate of its
// own, so it is only interpretable against a named host.
//
// The REFUSAL MOVED, and the move is worth recording rather than just
// re-pointing the assertion. This asserted ErrAppliedUnusable, which admit
// still raises for applied state with no coordinate. But Admit now refuses an
// unnamed host OUTRIGHT — a round-four review showed Host{}.Admit returning
// DecisionApply for an unlisted signer under an unlisted domain on a foreign
// coordinate, every provenance check skipped behind the `Coordinate != ""`
// guards that exist for AdmitRendered's sake. So the stronger check fires
// first and this condition is no longer reachable through the public surface.
//
// The ErrAppliedUnusable branch is KEPT as defence in depth on the internal
// admit, which AdmitRendered also calls, and is deliberately not asserted
// here: a test that reached it would have to call an unexported function, and
// the honest statement about the public surface is the one below.
func TestAppliedStateRequiresANamedHost(t *testing.T) {
	applied, err := solutionhost.AppliedFrom(parse(t, "valid"))
	require.NoError(t, err)

	_, err = solutionhost.Host{Applied: []solutionhost.Applied{applied}}.Admit(deliver(t, parse(t, "valid")))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "under its own coordinate")
	require.Contains(t, err.Error(), "AdmitRendered",
		"the refusal must name the entrypoint a caller with no host state should use")
}

// A host that starts reserving a namespace must not be frozen by a binding that
// was applied before the reservation existed. The reservation constrains what
// delivery asks for now, not what is already running.
func TestANewlyReservedNamespaceDoesNotBlockUnrelatedBindings(t *testing.T) {
	legacy := parse(t, "valid")
	legacy.Binding = "legacy-01"
	legacy.Routes = []solutionhost.Route{{Alias: "codefly/admin", Surface: solutionhost.SurfaceFrontend}}
	applied, err := solutionhost.AppliedFrom(legacy)
	require.NoError(t, err)

	host := solutionhost.Host{
		Coordinate:      solutionhost.FixtureCoordinate,
		Domains:         []string{solutionhost.FixtureDomain},
		DomainsBySigner: map[string][]string{fixtureDeliveredBy: {solutionhost.FixtureDomain, "beta", "not a domain"}},
		Reserved:        []string{"codefly"},
		Applied:         []solutionhost.Applied{applied},
	}

	fresh := parse(t, "valid")
	fresh.Binding = "alpha-02"
	fresh.Routes = []solutionhost.Route{{Alias: "brand-new", Surface: solutionhost.SurfaceFrontend}}
	decision, err := admitOne(t, host, fresh)
	require.NoError(t, err)
	require.Equal(t, solutionhost.DecisionApply, decision)

	// The incumbent is still refused the moment delivery redeclares it, which
	// is where an operator can act on the reservation.
	legacy.Generation++
	_, err = admitOne(t, host, legacy)
	require.ErrorIs(t, err, composition.ErrCollision)
}

// Validity is a per-document property, so one malformed binding must not
// freeze every other binding on the host.
//
// WHERE that is enforced moved, and the property is the same. A malformed
// document can no longer become Delivered at all: VerifyDelivered parses after
// the attestation holds, so it is refused at the delivery boundary and never
// reaches Admit. So the per-document guarantee is now "delivering one document
// fails that document", which is strictly earlier and strictly narrower than
// an admission error over a set.
//
// This is the invariant the withdrawn ByDomain guidance violated, from the
// other side: one unreadable document must never decide anything about a
// sound one.
func TestOneMalformedDocumentDoesNotRefuseTheRest(t *testing.T) {
	good := parse(t, "valid")
	bad := parse(t, "valid")
	bad.Binding = "typo-01"
	bad.Routes = nil
	bad.Generation = 0

	// The malformed one cannot be carried at all, let alone delivered or
	// admitted — and the refusal names its actual defect.
	//
	// Marshalled directly rather than through CanonicalBytes, because
	// CanonicalBytes validates too: an invalid document has no canonical
	// encoding. So there are three layers between a malformed document and
	// Admit now, and none of them is a set-wide decision.
	payload, err := json.Marshal(bad)
	require.NoError(t, err)
	_, err = solutionhost.Carrier(payload, json.RawMessage(solutionhost.FixtureBundle))
	require.Error(t, err)
	require.Contains(t, err.Error(), "generation starts at 1")

	// And the sound one still admits, with the malformed one simply absent
	// from the set rather than poisoning it.
	admissions, err := solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: good, FirstRecord: true})
	require.NoError(t, err)
	require.Len(t, admissions, 1)
	require.NoError(t, admissions[0].Err)
	require.Equal(t, good.Binding, admissions[0].Binding)
	require.Equal(t, solutionhost.DecisionApply, admissions[0].Decision)
}

// A redeclaration refused before the alias pass — here for being an older
// generation — never releases its applied alias either.
func TestARedeclarationRefusedForItsGenerationKeepsItsAppliedAlias(t *testing.T) {
	host := fixtureHost(t)
	require.Equal(t, []string{"alpha"}, host.Applied[0].Routes)

	// alpha-region-a-01 looks like it is releasing "alpha"… but the document is an
	// older generation and will be refused, so the applied one still holds it.
	stale := parse(t, "stale-generation")
	stale.Routes = nil
	stale.Artifacts = stale.Artifacts[:1]
	require.Equal(t, host.Applied[0].Binding, stale.Binding)

	claimant := parse(t, "duplicate-route-alias")

	admissions, err := host.Admit(deliverAll(t, stale, claimant)...)
	require.Error(t, err)
	require.Len(t, admissions, 2)
	require.ErrorIs(t, admissions[0].Err, solutionhost.ErrStaleGeneration)
	require.ErrorIs(t, admissions[1].Err, composition.ErrCollision,
		"the alias was never actually released, so the second claimant must be refused too")
}

// The alias pass must re-run after each refusal, not decide the whole set
// against one snapshot.
//
// aaa-01 passes every per-document check, so the first snapshot treats its
// applied generation as replaced and "alpha" as released — which lets bbb-01
// take it. Only when aaa-01 is then refused for claiming "zzz" does it become
// true that aaa-01 still holds "alpha", and bbb-01 must be refused too. A
// single-pass implementation admits bbb-01 here and collides at apply time.
func TestTheAliasPassRerunsAfterARefusalFreesNothing(t *testing.T) {
	incumbent := parse(t, "valid")
	incumbent.Binding = "aaa-01"
	incumbentApplied, err := solutionhost.AppliedFrom(incumbent)
	require.NoError(t, err)
	require.Equal(t, []string{"alpha"}, incumbentApplied.Routes)

	neighbour := parse(t, "valid")
	neighbour.Binding = "zzz-01"
	neighbour.Routes = []solutionhost.Route{{Alias: "zzz", Surface: solutionhost.SurfaceFrontend}}
	neighbourApplied, err := solutionhost.AppliedFrom(neighbour)
	require.NoError(t, err)

	host := appliedHost(incumbentApplied, neighbourApplied)

	// aaa-01 moves off "alpha" and onto an alias zzz-01 already holds.
	moving := parse(t, "valid")
	moving.Binding = "aaa-01"
	moving.Generation = incumbent.Generation + 1
	moving.Routes = []solutionhost.Route{{Alias: "zzz", Surface: solutionhost.SurfaceFrontend}}

	// bbb-01 wants the alias aaa-01 appeared to be releasing.
	successor := parse(t, "valid")
	successor.Binding = "bbb-01"
	successor.Generation = 1

	admissions, err := host.Admit(deliverAll(t, moving, successor)...)
	require.Error(t, err)
	require.Len(t, admissions, 2)
	require.ErrorIs(t, admissions[0].Err, composition.ErrCollision)
	require.ErrorIs(t, admissions[1].Err, composition.ErrCollision,
		"aaa-01 was refused, so it never released \"alpha\" and bbb-01 cannot have it")
}
