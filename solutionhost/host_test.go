package solutionhost_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codefly-dev/core/composition"
	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

func fixtureHost(t *testing.T) solutionhost.Host {
	t.Helper()
	host, err := solutionhost.FixtureHost()
	require.NoError(t, err)
	return host
}

// fixtureDeliveredBy is the signer identity the test bundle verifier attests, and
// the one FixtureHost and appliedHost let speak for the fixture domain.
const fixtureDeliveredBy = solutionhost.FixtureDeliveredBy

// testBundleVerifier stands in for the caller's sigstore-go verification. Core
// implements none, so every test that needs a Delivered document supplies one
// — which is the property under test as much as a convenience: there is no way
// to get a Delivered without one.
type testBundleVerifier struct {
	signer string
	err    error
}

func (v testBundleVerifier) VerifyBundle(_ context.Context, _ []byte, _ json.RawMessage) (string, error) {
	if v.err != nil {
		return "", v.err
	}
	signer := v.signer
	if signer == "" {
		signer = fixtureDeliveredBy
	}
	return signer, nil
}

// deliver wraps a parsed document as one whose carrier was verified, by going
// through the real VerifyDelivered path rather than constructing the wrapper
// — which a test outside the package cannot do anyway, and should not be able
// to.
func deliver(t *testing.T, document *solutionhost.SolutionHostBinding) *solutionhost.Delivered {
	t.Helper()
	return deliverSignedBy(t, document, fixtureDeliveredBy)
}

func deliverSignedBy(t *testing.T, document *solutionhost.SolutionHostBinding, signer string) *solutionhost.Delivered {
	t.Helper()
	payload, err := document.CanonicalBytes()
	require.NoError(t, err)
	carrier, err := solutionhost.Carrier(payload, json.RawMessage(solutionhost.FixtureBundle))
	require.NoError(t, err)
	delivered, err := solutionhost.VerifyDelivered(context.Background(), carrier, testBundleVerifier{signer: signer})
	require.NoError(t, err)
	return delivered
}

func deliverAll(t *testing.T, documents ...*solutionhost.SolutionHostBinding) []*solutionhost.Delivered {
	t.Helper()
	out := make([]*solutionhost.Delivered, len(documents))
	for index, document := range documents {
		out[index] = deliver(t, document)
	}
	return out
}

// admitOne drives the single-document case every host-side test uses and
// asserts the Admission agrees with the returned error.
func admitOne(t *testing.T, host solutionhost.Host, document *solutionhost.SolutionHostBinding) (solutionhost.Decision, error) {
	t.Helper()
	admissions, err := host.Admit(deliver(t, document))
	require.Len(t, admissions, 1)
	if err != nil {
		require.Error(t, admissions[0].Err)
		require.ErrorIs(t, err, admissions[0].Err)
		return "", err
	}
	require.NoError(t, admissions[0].Err)
	return admissions[0].Decision, nil
}

func parse(t *testing.T, name string) *solutionhost.SolutionHostBinding {
	t.Helper()
	return mustParse(t, presence(t, name))
}

// appliedHost is a host that has applied one record, carrying the domain and
// coordinate the fixtures target. Host.Domains is required whenever a
// coordinate is set, so every host-side test states what it accepts rather
// than relying on a permissive default.
func appliedHost(applied ...solutionhost.Applied) solutionhost.Host {
	return solutionhost.Host{
		Coordinate:      solutionhost.FixtureCoordinate,
		Domains:         []string{solutionhost.FixtureDomain},
		DomainsBySigner: map[string][]string{fixtureDeliveredBy: {solutionhost.FixtureDomain, "beta"}},
		Applied:         applied,
	}
}

// The whole conformance kit, driven the way codefly-dev/cli and the host in
// the host that reconciles these documents is expected to drive it.
func TestShippedFixturesReachTheirDeclaredOutcome(t *testing.T) {
	host := fixtureHost(t)
	fixtures := solutionhost.FixturesOf(solutionhost.DocumentTypePresence)
	require.Len(t, fixtures, 48)

	for _, shipped := range fixtures {
		t.Run(shipped.Name, func(t *testing.T) {
			require.NotEmpty(t, shipped.Reason)
			document, err := solutionhost.Parse(shipped.Document)
			if err != nil {
				// A fixture a single document's own rules reject never reaches
				// a host at all, which is itself the required outcome.
				require.Equal(t, solutionhost.OutcomeRejected, shipped.Outcome)
				return
			}
			decision, err := admitOne(t, host, document)
			if shipped.Outcome == solutionhost.OutcomeRejected {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, shipped.Decision, decision)
		})
	}
}

func TestEachRejectedFixtureNamesWhyItWasRejected(t *testing.T) {
	host := fixtureHost(t)
	for name, target := range map[string]error{
		"stale-generation":         solutionhost.ErrStaleGeneration,
		"duplicate-route-alias":    composition.ErrCollision,
		"tombstone-foreign-domain": solutionhost.ErrWrongDomain,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := admitOne(t, host, parse(t, name))
			require.ErrorIs(t, err, target)
		})
	}
	for name, target := range map[string]error{
		"mixed-release":       solutionhost.ErrMixedRelease,
		"wrong-kind":          solutionhost.ErrInvalid,
		"missing-identity":    solutionhost.ErrInvalid,
		"digest-confusion":    solutionhost.ErrDigestConfusion,
		"other-document-type": solutionhost.ErrSchema,
		// Addressing is the second axis of an endpoint's declaration, and each
		// of its three refusals is the resource model's own, made where the
		// document is read.
		"endpoint-exposure-omitted":      solutionhost.ErrInvalid,
		"endpoint-exposure-beyond-reach": solutionhost.ErrInvalid,
		"endpoint-exposure-unknown":      solutionhost.ErrInvalid,
		// The build-size section's refusals, each one named rule; see
		// TestEveryBuildSizeRuleIsProtectedByAFixture for the rule each
		// protects, and TestEachRejectedPresenceFixtureIsRefusedNamingItsMessage
		// for the text each carries.
		"build-size-languages-omitted":        solutionhost.ErrInvalid,
		"build-size-unknown-language":         solutionhost.ErrInvalid,
		"build-size-language-twice":           solutionhost.ErrInvalid,
		"build-size-empty-language":           solutionhost.ErrInvalid,
		"build-size-vendored-omitted":         solutionhost.ErrInvalid,
		"build-size-vendored-trailing-slash":  solutionhost.ErrInvalid,
		"build-size-vendored-twice":           solutionhost.ErrInvalid,
		"build-size-vendored-nested":          solutionhost.ErrInvalid,
		"build-size-backend-total-disagrees":  solutionhost.ErrInvalid,
		"build-size-frontend-total-disagrees": solutionhost.ErrInvalid,
		"build-size-total-disagrees":          solutionhost.ErrInvalid,
		"tombstone-with-build-size":           solutionhost.ErrInvalid,
		"not-yaml":                            solutionhost.ErrInvalid,
		"two-documents":                       solutionhost.ErrInvalid,
		"build-size-unknown-field":            solutionhost.ErrInvalid,
		"build-size-field-twice":              solutionhost.ErrInvalid,
		"build-size-count-null":               solutionhost.ErrInvalid,
		"build-size-count-fractional":         solutionhost.ErrInvalid,
		"build-size-empty-key":                solutionhost.ErrInvalid,
		"build-size-total-omitted":            solutionhost.ErrInvalid,
		"build-size-row-frontend-omitted":     solutionhost.ErrInvalid,
		"build-size-count-hex":                solutionhost.ErrInvalid,
		"build-size-count-leading-zero":       solutionhost.ErrInvalid,
		"build-size-count-negative":           solutionhost.ErrInvalid,
		"build-size-count-too-large":          solutionhost.ErrInvalid,
		"build-size-backend-sum-overflows":    solutionhost.ErrInvalid,
		"build-size-frontend-sum-overflows":   solutionhost.ErrInvalid,
		"build-size-total-overflows":          solutionhost.ErrInvalid,
		"build-size-vendored-not-utf8":        solutionhost.ErrInvalid,
		"build-size-merge-key":                solutionhost.ErrInvalid,
		"merge-key-at-top-level":              solutionhost.ErrInvalid,
		"build-size-binary-key":               solutionhost.ErrInvalid,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := solutionhost.Parse(presence(t, name))
			require.ErrorIs(t, err, target)
		})
	}
}

func TestARereadOfTheAppliedGenerationIsCurrentAndARewriteIsAnError(t *testing.T) {
	host := fixtureHost(t)

	decision, err := admitOne(t, host, parse(t, "valid"))
	require.NoError(t, err)
	require.Equal(t, solutionhost.DecisionCurrent, decision)

	rewritten := parse(t, "valid")
	rewritten.Artifacts[0].Digest = solutionhost.RenderedDigest("sha256:" + strings.Repeat("0", 64))
	_, err = admitOne(t, host, rewritten)
	require.ErrorIs(t, err, solutionhost.ErrRewrittenGeneration)
}

func TestATombstoneReleasesItsAliasForANewInstance(t *testing.T) {
	host := fixtureHost(t)

	tombstone := parse(t, "tombstone")
	decision, err := admitOne(t, host, tombstone)
	require.NoError(t, err)
	require.Equal(t, solutionhost.DecisionApply, decision)

	applied, err := solutionhost.AppliedFrom(tombstone)
	require.NoError(t, err)
	require.True(t, applied.Removed)
	require.Empty(t, applied.Routes)

	// Once the tombstone is the applied generation, the alias is free — but the
	// binding ID is not: its generation history survives removal.
	removed := appliedHost(applied)
	_, err = admitOne(t, removed, parse(t, "duplicate-route-alias"))
	require.NoError(t, err)
	_, err = admitOne(t, removed, parse(t, "valid"))
	require.ErrorIs(t, err, solutionhost.ErrStaleGeneration)
}

func TestABindingMovingItsOwnAliasDoesNotCollideWithItself(t *testing.T) {
	host := fixtureHost(t)

	next := parse(t, "valid")
	next.Generation = 5
	next.Routes = []solutionhost.Route{{Alias: "alpha/v2", Surface: solutionhost.SurfaceFrontend}}

	decision, err := admitOne(t, host, next)
	require.NoError(t, err)
	require.Equal(t, solutionhost.DecisionApply, decision)
}

func TestARendererChecksASetBeforeItIsDelivered(t *testing.T) {
	first := parse(t, "valid")
	second := parse(t, "duplicate-route-alias")

	// The zero Host is the renderer's view: nothing applied, no coordinate
	// pinned, and the collision still refused where the set was authored.
	_, err := solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: first, FirstRecord: true}, solutionhost.RenderedSet{Document: second, FirstRecord: true})
	require.ErrorIs(t, err, composition.ErrCollision)
	require.Contains(t, err.Error(), first.Binding)
	require.Contains(t, err.Error(), second.Binding)

	second.Routes[0].Alias = "alpha2"
	admissions, err := solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: first, FirstRecord: true}, solutionhost.RenderedSet{Document: second, FirstRecord: true})
	require.NoError(t, err)
	// RenderedAdmission, not Admission: AdmitRendered answering a host's own
	// type was C4's fail-open under another name. Fold is the generation
	// decision, DecisionApply here because no record was supplied.
	require.Equal(t, []solutionhost.RenderedAdmission{
		{Binding: first.Binding, Decision: solutionhost.DecisionApply, Fold: solutionhost.DecisionApply},
		{Binding: second.Binding, Decision: solutionhost.DecisionApply, Fold: solutionhost.DecisionApply},
	}, admissions)

	// An empty set is "nothing declared", not "remove everything": removal is a
	// generation, so Admit has nothing to say about it.
	admissions, err = solutionhost.AdmitRenderedSets()
	require.NoError(t, err)
	require.Empty(t, admissions)
}

func TestOneSetDeclaresOneGenerationPerBinding(t *testing.T) {
	first := parse(t, "valid")
	second := parse(t, "valid")
	second.Generation = 6
	second.Routes = nil

	_, err := solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: first, FirstRecord: true}, solutionhost.RenderedSet{Document: second, FirstRecord: true})
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "declared twice")
}

func TestADocumentForAnotherHostIsRefused(t *testing.T) {
	document := parse(t, "valid")
	document.Host.Coordinate = "example/prod/region-b"

	_, err := admitOne(t, fixtureHost(t), document)
	require.ErrorIs(t, err, solutionhost.ErrWrongHost)
	require.Contains(t, err.Error(), "region-b")
}

func TestAHostReservesRouteNamespaces(t *testing.T) {
	host := solutionhost.Host{
		Coordinate:      solutionhost.FixtureCoordinate,
		Domains:         []string{solutionhost.FixtureDomain},
		DomainsBySigner: map[string][]string{fixtureDeliveredBy: {solutionhost.FixtureDomain, "beta", "not a domain"}},
		Reserved:        []string{"codefly"},
	}

	document := parse(t, "valid")
	document.Routes = []solutionhost.Route{{Alias: "codefly/admin", Surface: solutionhost.SurfaceFrontend}}
	_, err := admitOne(t, host, document)
	require.ErrorIs(t, err, composition.ErrCollision)

	document.Routes = []solutionhost.Route{{Alias: "codeflyer", Surface: solutionhost.SurfaceFrontend}}
	_, err = admitOne(t, host, document)
	require.NoError(t, err)
}

// Unusable applied state is refused, and refused as the HOST's problem.
//
// Both halves are asserted. A host consumer hit the second: a host
// that had not yet persisted Applied.Domain got "solution host document is
// invalid", which blames a delivered document for state only the host can
// repair — and because applied state is read once for the whole set, one bad
// record withholds every binding on every pass while the error points at
// delivery. So each case pins ErrAppliedUnusable and pins that it is NOT
// ErrInvalid; an assertion of only "some error" would have passed throughout
// the whole time the accusation was wrong.
func TestInvalidAppliedStateIsRejectedRatherThanTrusted(t *testing.T) {
	document := parse(t, "valid")
	digest, err := document.Digest()
	require.NoError(t, err)
	domain := solutionhost.FixtureDomain
	sound := solutionhost.Applied{
		Binding: document.Binding, Generation: 4, Digest: digest, Domain: domain, Routes: []string{"alpha"},
	}

	for name, applied := range map[string][]solutionhost.Applied{
		"no binding ID":   {{Generation: 1, Digest: digest, Domain: domain}},
		"generation zero": {{Binding: "other-01", Generation: 0, Digest: digest, Domain: domain}},
		"no digest":       {{Binding: "other-01", Generation: 1, Domain: domain}},
		// A record with no domain is a record no document can be held
		// against: the ownership check would pass for any domain a delivery
		// chose to write.
		"no ownership domain": {{Binding: "other-01", Generation: 1, Digest: digest}},
		"recorded twice":      {sound, sound},
		"tombstone holding a route": {{
			Binding: "other-01", Generation: 1, Digest: digest, Domain: domain,
			Routes: []string{"other"}, Removed: true,
		}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := appliedHost(applied...).Admit(deliverAll(t, document)...)
			require.ErrorIs(t, err, solutionhost.ErrAppliedUnusable)
			require.NotErrorIs(t, err, solutionhost.ErrInvalid,
				"no delivery can repair the host's own stored state, so this must not read as a delivery problem")
		})
	}

	// Two applied records holding one alias is a COLLISION, not an unusable
	// record: each record is well formed and the conflict is between them, so
	// it reads the same as every other composition collision.
	_, err = appliedHost(sound, solutionhost.Applied{
		Binding: "other-01", Generation: 1, Digest: digest, Domain: domain, Routes: []string{"alpha"},
	}).Admit(deliverAll(t, document)...)
	require.ErrorIs(t, err, composition.ErrCollision)
}

func TestAppliedFromRecordsWhatTheHostMustPersist(t *testing.T) {
	document := parse(t, "valid")
	applied, err := solutionhost.AppliedFrom(document)
	require.NoError(t, err)

	digest, err := document.Digest()
	require.NoError(t, err)
	require.Equal(t, solutionhost.Applied{
		Binding: document.Binding, Generation: 4, Digest: digest,
		Domain: solutionhost.FixtureDomain, Routes: []string{"alpha"},
	}, applied)

	document.Generation = 0
	_, err = solutionhost.AppliedFrom(document)
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
}

// A set is one delivery when every document in it speaks for one ownership
// domain, and that is a separate question from what a host may admit.
//
// A renderer writing one delivery asks it: a set straddling two domains cannot
// satisfy "within D the delivered set is exactly desired" for any single D, so
// removal within it is not expressible.
func TestOneDeliverySpeaksForOneDomain(t *testing.T) {
	first := parse(t, "valid")
	second := parse(t, "module-presence")
	require.NoError(t, solutionhost.OneDelivery(first, second), "both speak for the fixture domain")
	require.NoError(t, solutionhost.OneDelivery(), "an empty set straddles nothing")
	require.NoError(t, solutionhost.OneDelivery(first))

	second.OwnershipDomain = "beta"
	err := solutionhost.OneDelivery(first, second)
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)
	require.Contains(t, err.Error(), "beta")
	require.Contains(t, err.Error(), solutionhost.FixtureDomain)

	// A document that does not validate is its own refusal and never decides
	// the set's: otherwise one malformed document would withhold a delivery.
	malformed := parse(t, "valid")
	malformed.OwnershipDomain = "not a domain"
	require.NoError(t, solutionhost.OneDelivery(first, malformed))
}

// A host's mount is the union of however many deliveries reached it, so it
// legitimately carries one domain per delivery. Admit must not refuse that,
// which is the whole reason OneDelivery is a separate call.
func TestAHostAdmitsAMountHoldingSeveralDeliveries(t *testing.T) {
	fromAlpha := parse(t, "valid")
	fromBeta := parse(t, "module-presence")
	fromBeta.OwnershipDomain = "beta"
	fromBeta.Routes = nil

	host := solutionhost.Host{
		Coordinate:      solutionhost.FixtureCoordinate,
		Domains:         []string{solutionhost.FixtureDomain, "beta"},
		DomainsBySigner: map[string][]string{fixtureDeliveredBy: {solutionhost.FixtureDomain, "beta", "not a domain"}},
		Applied:         fixtureHost(t).Applied,
	}
	admissions, err := host.Admit(deliverAll(t, fromAlpha, fromBeta)...)
	require.NoError(t, err, "two deliveries into one mount is the normal case, not a straddle")
	require.Equal(t, solutionhost.DecisionCurrent, admissions[0].Decision)
	require.Equal(t, solutionhost.DecisionApply, admissions[1].Decision)

	// A domain the host does not accept is refused per document, which is the
	// rule that bounds a binding's first generation.
	narrow := solutionhost.Host{
		Coordinate:      solutionhost.FixtureCoordinate,
		Domains:         []string{solutionhost.FixtureDomain},
		DomainsBySigner: map[string][]string{fixtureDeliveredBy: {solutionhost.FixtureDomain, "beta", "not a domain"}},
		Applied:         fixtureHost(t).Applied,
	}
	_, err = admitOne(t, narrow, fromBeta)
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)
	require.Contains(t, err.Error(), "does not accept")
}

// A named host must say which domains it accepts. An unstated list would accept
// every domain, which is the hole the field exists to close — so it is an error
// rather than a permissive default. A renderer leaves both empty.
func TestANamedHostMustDeclareTheDomainsItAccepts(t *testing.T) {
	_, err := solutionhost.Host{Coordinate: solutionhost.FixtureCoordinate}.Admit(deliver(t, parse(t, "valid")))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "ownership domains it accepts")

	_, err = solutionhost.Host{
		Coordinate:      solutionhost.FixtureCoordinate,
		Domains:         []string{"not a domain"},
		DomainsBySigner: map[string][]string{fixtureDeliveredBy: {solutionhost.FixtureDomain, "beta", "not a domain"}},
	}.Admit(deliver(t, parse(t, "valid")))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)

	// The renderer's view, unaffected: no coordinate, no domains, and every
	// check that does not need host state still runs.
	_, err = solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: parse(t, "valid"), FirstRecord: true})
	require.NoError(t, err)
}

// A binding keeps the domain it was applied under, and a tombstone is the case
// that matters: otherwise any delivery the host accepts could withdraw any
// binding by declaring a higher generation under its own domain.
func TestABindingKeepsTheDomainItWasAppliedUnder(t *testing.T) {
	host := solutionhost.Host{
		Coordinate:      solutionhost.FixtureCoordinate,
		Domains:         []string{solutionhost.FixtureDomain, "beta"},
		DomainsBySigner: map[string][]string{fixtureDeliveredBy: {solutionhost.FixtureDomain, "beta", "not a domain"}},
		Applied:         fixtureHost(t).Applied,
	}

	// The host accepts "beta", so this is refused on ownership and not on
	// acceptance — which is what makes the two rules distinct.
	foreign := parse(t, "tombstone-foreign-domain")
	require.Equal(t, "beta", foreign.OwnershipDomain)
	require.Greater(t, foreign.Generation, host.Applied[0].Generation)

	_, err := admitOne(t, host, foreign)
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)
	require.Contains(t, err.Error(), "was applied under domain")

	// The same withdrawal from the delivery that owns the binding applies.
	_, err = admitOne(t, host, parse(t, "tombstone"))
	require.NoError(t, err)
}

// A tombstoned binding ID is TERMINAL. No later generation reapplies it.
//
// decide used to let any higher generation resurrect one, and nothing tested
// it. The binding ID is the handle every other system holds — installations,
// operation bindings, team grants — so reusing it after a withdrawal is
// indistinguishable from continuity, which is the one thing a withdrawal is
// supposed to make distinguishable. A replacement instance needs a new ID,
// which is a rename at the renderer rather than a loss.
func TestATombstonedBindingIsNotResurrectedByAHigherGeneration(t *testing.T) {
	document := parse(t, "valid")
	tombstone := parse(t, "tombstone")
	withdrawn, err := solutionhost.AppliedFrom(tombstone)
	require.NoError(t, err)
	require.True(t, withdrawn.Removed)

	revival := parse(t, "valid")
	revival.Binding = withdrawn.Binding
	revival.Generation = withdrawn.Generation + 1

	host := solutionhost.Host{
		Coordinate:      solutionhost.FixtureCoordinate,
		Domains:         []string{solutionhost.FixtureDomain},
		DomainsBySigner: map[string][]string{fixtureDeliveredBy: {solutionhost.FixtureDomain, "beta", "not a domain"}},
		Applied:         []solutionhost.Applied{withdrawn},
	}
	_, err = admitOne(t, host, revival)
	require.ErrorIs(t, err, solutionhost.ErrTombstoned)
	require.Contains(t, err.Error(), "replacement needs a new ID")

	// A genuinely new instance, with its own ID, is admitted.
	fresh := parse(t, "valid")
	fresh.Binding = "alpha-region-a-02"
	fresh.Routes = nil
	_ = document
	admissions, err := host.Admit(deliverAll(t, fresh)...)
	require.NoError(t, err)
	require.Equal(t, solutionhost.DecisionApply, admissions[0].Decision)
}

// A document asserts its own ownership domain, so the host must say WHO may
// make that assertion.
//
// Domains alone bounded which domains the host accepts at all. It did not
// bound who may speak for one, so any signer the host accepted could write any
// accepted domain and take over bindings in it — the keyless form of "a
// document nominates its own authority", one layer up from the key.
func TestADomainIsOnlyDeliverableByASignerTheHostAllows(t *testing.T) {
	document := parse(t, "valid")
	host := solutionhost.Host{
		Coordinate:      solutionhost.FixtureCoordinate,
		Domains:         []string{solutionhost.FixtureDomain},
		DomainsBySigner: map[string][]string{solutionhost.FixtureDeliveredBy: {solutionhost.FixtureDomain}},
	}

	// The allowed signer delivers it.
	admissions, err := host.Admit(deliver(t, document))
	require.NoError(t, err)
	require.Equal(t, solutionhost.DecisionApply, admissions[0].Decision)

	// Another identity the host has never heard of, delivering the SAME sound
	// document under a domain the host DOES accept. Everything about the
	// document is fine; only the identity that attested it is not.
	_, err = host.Admit(deliverSignedBy(t, document, "https://signer.example/someone-else@refs/heads/main"))
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)
	require.Contains(t, err.Error(), "does not let speak for it")
}

// A named host must declare the signer policy too. An unstated one would let
// every accepted signer speak for every accepted domain, which is the hole the
// field exists to close.
func TestANamedHostMustDeclareWhoMaySpeakForItsDomains(t *testing.T) {
	_, err := solutionhost.Host{
		Coordinate: solutionhost.FixtureCoordinate,
		Domains:    []string{solutionhost.FixtureDomain},
	}.Admit(deliver(t, parse(t, "valid")))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "which signer identities may deliver under which domains")
}

// There is no way to reach Admit without an attestation having held. The
// workcontext half of this change uses distinct types for exactly this reason;
// the document half had been relying on "verified" being part of a function
// name.
func TestAdmitCannotBeReachedWithoutABundleVerifier(t *testing.T) {
	document := parse(t, "valid")
	payload, err := document.CanonicalBytes()
	require.NoError(t, err)
	carrier, err := solutionhost.Carrier(payload, json.RawMessage(solutionhost.FixtureBundle))
	require.NoError(t, err)

	// No verifier at all: core will not treat its absence as one holding.
	_, err = solutionhost.VerifyDelivered(context.Background(), carrier, nil)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.Contains(t, err.Error(), "will not treat its absence as one holding")

	// A verifier that refuses.
	_, err = solutionhost.VerifyDelivered(context.Background(), carrier,
		testBundleVerifier{err: errors.New("certificate identity not allowed")})
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.Contains(t, err.Error(), "the attestation does not hold")

	// A verifier that accepts but names nobody: nothing to map to a domain.
	_, err = solutionhost.VerifyDelivered(context.Background(), carrier, testBundleVerifier{signer: " "})
	require.NoError(t, err, "a blank-but-present identity is the caller's business")
}

// A renderer checks a set it is about to write, over PARSED documents, with no
// host and no attestation.
//
// This entrypoint exists because making Admit take *Delivered broke the
// renderer and the break was invisible from inside core: a renderer's
// documents are not signed yet, so there is no carrier to verify and no way to
// reach the zero-host checks. A consumer reported it by starting to
// re-implement them, which is the two-implementations failure this package
// exists to end, appearing in the fix for it.
func TestAdmitRenderedIsTheRenderersHalfAndNeedsNoAttestation(t *testing.T) {
	first := parse(t, "valid")
	second := parse(t, "module-presence")
	second.Routes = nil

	admissions, err := solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: first, FirstRecord: true}, solutionhost.RenderedSet{Document: second, FirstRecord: true})
	require.NoError(t, err)
	require.Len(t, admissions, 2)
	for _, admission := range admissions {
		require.NoError(t, admission.Err)
		require.Equal(t, solutionhost.DecisionApply, admission.Decision)
	}

	// It still refuses everything that needs no host: a binding declared
	// twice in one set...
	_, err = solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: first, FirstRecord: true}, solutionhost.RenderedSet{Document: parse(t, "valid"), FirstRecord: true})
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "declared twice")

	// ...and two documents on one host claiming the same route alias. Both
	// are complete presence documents, so the refusal is the collision rather
	// than either document being unsound.
	colliding := parse(t, "valid")
	colliding.Binding = "alpha-region-a-09"
	_, err = solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: first, FirstRecord: true}, solutionhost.RenderedSet{Document: colliding, FirstRecord: true})
	require.ErrorIs(t, err, composition.ErrCollision)
}

// AdmitRendered cannot be handed host state, because it takes no Host. That is
// what keeps the invariant: there is no sequence of calls that reaches a
// HOST's Admit without an attestation having held.
func TestAdmitRenderedCannotBeHandedHostState(t *testing.T) {
	// The only way to check a coordinate, a domain, a signer policy or an
	// applied record is through Host.Admit, which takes *Delivered.
	host := solutionhost.Host{
		Coordinate:      solutionhost.FixtureCoordinate,
		Domains:         []string{solutionhost.FixtureDomain},
		DomainsBySigner: map[string][]string{solutionhost.FixtureDeliveredBy: {solutionhost.FixtureDomain}},
	}
	_, err := host.Admit(deliver(t, parse(t, "valid")))
	require.NoError(t, err)

	// And AdmitRendered reaches none of those checks, so it cannot pass or
	// fail them: a wrong-coordinate document is fine by it, because a renderer
	// has no host to be wrong about.
	elsewhere := parse(t, "valid")
	elsewhere.Host.Coordinate = "example/prod/region-z"
	_, err = solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: elsewhere, FirstRecord: true})
	require.NoError(t, err, "a renderer pre-checking a set has no host to answer coordinate questions for")
}

// A verified document cannot be mutated after the fact: Document() re-derives
// from the ATTESTED bytes and returns a fresh value, and Admit reads those
// bytes rather than anything a caller has held.
//
// Measured before this: `delivered.Document().Generation += 7` then
// `host.Admit(delivered)` admitted generation 11 while the attestation
// covered 4 — the verification real and the admitted content mutable. A caller
// did not have to be malicious; anything normalising a field for its own
// bookkeeping would do it.
func TestAVerifiedDocumentCannotBeMutatedAfterTheAttestation(t *testing.T) {
	delivered := deliver(t, parse(t, "valid"))

	first, err := delivered.Document()
	require.NoError(t, err)
	attested := first.Generation
	first.Generation = attested + 7
	first.OwnershipDomain = "beta"

	// A second read is unaffected by the first caller's mutation.
	second, err := delivered.Document()
	require.NoError(t, err)
	require.Equal(t, attested, second.Generation)
	require.Equal(t, solutionhost.FixtureDomain, second.OwnershipDomain)

	// And Admit consumes the attested content, not the mutated value: the
	// mutated domain would have been refused by the signer policy, and the
	// mutated generation would have read as a later one.
	admissions, err := appliedHost().Admit(delivered)
	require.NoError(t, err)
	require.Equal(t, solutionhost.DecisionApply, admissions[0].Decision)
	require.Equal(t, second.Binding, admissions[0].Binding)
}

// An outside-constructed Delivered carries no attested bytes, so it yields no
// document — a refusal rather than the nil-pointer panic Activate used to take.
func TestAnOutsideConstructedDeliveredYieldsNothing(t *testing.T) {
	_, err := (&solutionhost.Delivered{}).Document()
	require.Error(t, err)
	_, err = (&solutionhost.DeliveredAuthority{}).Document()
	require.Error(t, err)

	_, err = solutionhost.Activate(solutionhost.ActivationRequest{
		Coordinate: solutionhost.FixtureCoordinate,
		Authority:  &solutionhost.DeliveredAuthority{},
		Presence:   deliver(t, parse(t, "valid")),
		Build:      parse(t, "valid").Workloads[0].Image.Digest,
		Envelope:   solutionhost.FixtureEnvelope(),
		// The policy is supplied because this test's subject is the
		// re-derivation, and without it the signer-policy refusal now comes
		// first. That this test used to pass none is itself evidence of the
		// hole: a request assembled with no policy at all was accepted all
		// the way to the document checks.
		DomainsBySigner: map[string][]string{solutionhost.FixtureDeliveredBy: {solutionhost.FixtureDomain}},
	})
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "does not re-derive")
}

// TestADeliveredIsImmutableThroughEveryAccessor covers S7, S8 and S9, whose
// guards had no committed test — a round-four review found all three
// deletable with the suite green, which is the same as never having written
// them.
//
// The shape is one shape, and it has now appeared on four types in two
// packages: a verified value that hands out a reference to its own state lets
// a holder edit what verification already approved. Here it is the attested
// payload — if VerifyDelivered stored the caller's slice, or Payload() handed
// back the stored one, or DeliveredAuthority.Document() returned a shared
// message, then the bytes a signature held over stop being the bytes a reader
// gets.
func TestADeliveredIsImmutableThroughEveryAccessor(t *testing.T) {
	document := valid(t)
	payload, err := document.CanonicalBytes()
	require.NoError(t, err)
	carrier, err := solutionhost.Carrier(payload, json.RawMessage(solutionhost.FixtureBundle))
	require.NoError(t, err)

	delivered, err := solutionhost.VerifyDelivered(context.Background(), carrier, testBundleVerifier{})
	require.NoError(t, err)
	original, err := delivered.Document()
	require.NoError(t, err)

	// S8: the caller's slice is cloned on the way IN.
	//
	// THE FIRST VERSION OF THIS SCRIBBLED `payload`, WHICH PROVES NOTHING.
	// Carrier() already clones the payload into Signed.Document, so the local
	// buffer can never reach the stored bytes whether or not VerifyDelivered
	// clones — mutation-verified: storing the caller's slice left the suite
	// green. The buffer that discriminates is carrier.Document, which is what
	// VerifyDelivered is actually handed.
	for index := range carrier.Document {
		carrier.Document[index] = 'X'
	}
	after, err := delivered.Document()
	require.NoError(t, err)
	require.Equal(t, original.Binding, after.Binding, "VerifyDelivered stored the caller's slice")
	require.Equal(t, original.Generation, after.Generation)

	// S9: Payload() hands out a clone on the way OUT.
	held := delivered.Payload()
	for index := range held {
		held[index] = 'Y'
	}
	again, err := delivered.Document()
	require.NoError(t, err)
	require.Equal(t, original.Generation, again.Generation, "Payload() aliased the attested bytes")

	// And the document is re-derived per read, so editing one changes nothing.
	original.Generation += 7
	fresh, err := delivered.Document()
	require.NoError(t, err)
	require.NotEqual(t, original.Generation, fresh.Generation)

	// S7: the same three properties on the AUTHORITY half, which is where the
	// aliasing was found rather than on the presence half.
	authorityDocument := validAuthority(t)
	authorityPayload, err := authorityDocument.CanonicalBytes()
	require.NoError(t, err)
	authorityCarrier, err := solutionhost.Carrier(authorityPayload, json.RawMessage(solutionhost.FixtureBundle))
	require.NoError(t, err)
	deliveredAuthority, err := solutionhost.VerifyDeliveredAuthority(context.Background(), authorityCarrier, testBundleVerifier{})
	require.NoError(t, err)

	firstRead, err := deliveredAuthority.Document()
	require.NoError(t, err)
	firstRead.Generation += 7
	firstRead.ApprovedBuild = solutionhost.ImageDigest("sha256:" + strings.Repeat("f", 64))
	secondRead, err := deliveredAuthority.Document()
	require.NoError(t, err)
	require.Equal(t, authorityDocument.Generation, secondRead.Generation,
		"DeliveredAuthority.Document() returned a shared message")
	require.Equal(t, authorityDocument.ApprovedBuild, secondRead.ApprovedBuild)

	// Same correction on the authority half: scribble the carrier's buffer,
	// not the one Carrier already copied.
	for index := range authorityCarrier.Document {
		authorityCarrier.Document[index] = 'X'
	}
	thirdRead, err := deliveredAuthority.Document()
	require.NoError(t, err)
	require.Equal(t, authorityDocument.Generation, thirdRead.Generation)
}

// TestVerifyDeliveredValidatesTheCarrierItself is S10: verifyCarrier calls
// carrier.validate(), and nothing held it.
//
// Without it a hand-built Signed goes straight to a BundleVerifier with
// whatever shape it likes — which is how a consumer's fixtures would pass
// locally and fail on a real carrier, since the published path through
// MarshalSigned/ParseSigned enforces the same rules.
func TestVerifyDeliveredValidatesTheCarrierItself(t *testing.T) {
	document := valid(t)
	payload, err := document.CanonicalBytes()
	require.NoError(t, err)
	sound, err := solutionhost.Carrier(payload, json.RawMessage(solutionhost.FixtureBundle))
	require.NoError(t, err)

	for name, break_ := range map[string]func(*solutionhost.Signed){
		"no schema":    func(s *solutionhost.Signed) { s.Schema = "" },
		"wrong schema": func(s *solutionhost.Signed) { s.Schema = "codefly/not-a-carrier/v1" },
		"no document":  func(s *solutionhost.Signed) { s.Document = nil },
		"no bundle":    func(s *solutionhost.Signed) { s.Bundle = nil },
	} {
		t.Run(name, func(t *testing.T) {
			broken := *sound
			break_(&broken)
			_, err := solutionhost.VerifyDelivered(context.Background(), &broken, testBundleVerifier{})
			require.Error(t, err, "a malformed carrier reached the verifier")
		})
	}
}

// TestARendererFoldsItsOwnGenerationsAndDomainContinuity closes the gap a
// renderer consumer reported by restating one of these rules in its own code.
//
// AdmitRendered takes no Host and therefore no applied records, so it ran no
// generation fold at all; ActivateRendered folds presence only as half of a
// pair, so a module with presence and no contract had no entrypoint for it.
// The consumer was duplicating the domain-continuity rule as a result — which
// is the duplication this package exists to prevent, and the gap was in this
// surface rather than in their reading of it.
func TestARendererFoldsItsOwnGenerationsAndDomainContinuity(t *testing.T) {
	current := valid(t)
	applied, err := solutionhost.AppliedFrom(current)
	require.NoError(t, err)

	next := valid(t)
	next.Generation = current.Generation + 1
	admissions, err := solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{
		Document: next, Applied: applied,
	})
	require.NoError(t, err)
	require.NoError(t, admissions[0].Err)
	require.Equal(t, solutionhost.DecisionApply, admissions[0].Fold)

	// A STALE generation is refused — the fold AdmitRendered could not run.
	stale := valid(t)
	stale.Generation = current.Generation - 1
	// THE RETURNED ERROR, not only the per-set one: this test pinned
	// `require.NoError(t, err)` for refused folds, so a caller checking only
	// the error — which is what Admit's contract trains — read a refusal as
	// success. And Decision must be CLEARED, or the host-free "apply" is
	// retained beside a refused fold.
	admissions, err = solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{
		Document: stale, Applied: applied,
	})
	require.ErrorIs(t, err, solutionhost.ErrStaleGeneration)
	require.ErrorIs(t, admissions[0].Err, solutionhost.ErrStaleGeneration)
	require.Empty(t, admissions[0].Decision, "a refused set retains no decision")
	require.Empty(t, admissions[0].Fold)

	// DOMAIN CONTINUITY, the rule the consumer was restating: the applied
	// record's domain says who may change this binding, so a document under
	// another domain is refused whatever its generation.
	moved := valid(t)
	moved.Generation = current.Generation + 1
	moved.OwnershipDomain = "beta"
	admissions, err = solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{
		Document: moved, Applied: applied,
	})
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)
	require.ErrorIs(t, admissions[0].Err, solutionhost.ErrWrongDomain)
	require.Empty(t, admissions[0].Decision)

	// A record for ANOTHER binding is not this one's history, and a record
	// that is not well formed is refused rather than folded.
	for name, set := range map[string]solutionhost.RenderedSet{
		"other binding": {Document: next, Applied: solutionhost.Applied{Binding: "elsewhere", Generation: 1}},
		"hand built":    {Document: next, Applied: solutionhost.Applied{Binding: next.Binding, Generation: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			admissions, err := solutionhost.AdmitRenderedSets(set)
			require.ErrorIs(t, err, solutionhost.ErrAppliedUnusable)
			require.ErrorIs(t, admissions[0].Err, solutionhost.ErrAppliedUnusable)
			require.Empty(t, admissions[0].Decision)
		})
	}

	// And "no record" is STATED, never defaulted — the same rule activation
	// states per half.
	admissions, err = solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: next})
	require.ErrorIs(t, err, solutionhost.ErrAppliedUnusable)
	require.ErrorIs(t, admissions[0].Err, solutionhost.ErrAppliedUnusable)
	require.Empty(t, admissions[0].Decision)
	require.Contains(t, admissions[0].Err.Error(), "declare that none exists")

	admissions, err = solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{
		Document: next, FirstRecord: true,
	})
	require.NoError(t, err)
	require.NoError(t, admissions[0].Err)
	require.Equal(t, solutionhost.DecisionApply, admissions[0].Fold)

	// A marker AND a record is a caller asserting two things.
	admissions, err = solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{
		Document: next, Applied: applied, FirstRecord: true,
	})
	require.ErrorIs(t, err, solutionhost.ErrAppliedUnusable)
	require.ErrorIs(t, admissions[0].Err, solutionhost.ErrAppliedUnusable)
	require.Empty(t, admissions[0].Decision)
}

// TestADeliveredAuthorityPayloadIsACopy is S9's authority half, which had no
// test — the presence half did, and the authority half is where the aliasing
// was originally found.
func TestADeliveredAuthorityPayloadIsACopy(t *testing.T) {
	document := validAuthority(t)
	payload, err := document.CanonicalBytes()
	require.NoError(t, err)
	carrier, err := solutionhost.Carrier(payload, json.RawMessage(solutionhost.FixtureBundle))
	require.NoError(t, err)
	delivered, err := solutionhost.VerifyDeliveredAuthority(context.Background(), carrier, testBundleVerifier{})
	require.NoError(t, err)

	held := delivered.Payload()
	require.NotEmpty(t, held)
	for index := range held {
		held[index] = 'Z'
	}
	again, err := delivered.Document()
	require.NoError(t, err)
	require.Equal(t, document.Authority, again.Authority,
		"DeliveredAuthority.Payload() handed out the attested bytes themselves")
	require.Equal(t, document.ApprovedBuild, again.ApprovedBuild)

	// Two reads are two copies, so one caller's edit cannot reach another's.
	first := delivered.Payload()
	first[0] = 'Q'
	second := delivered.Payload()
	require.NotEqual(t, first[0], second[0])
}

// TestABlankDomainRecordDoesNotDisableContinuity is R6-2: both reviewers found
// that `Domain != "" &&` let a record with a blank domain disable domain
// continuity entirely — in activation AND in the rendered fold, while
// Host.Admit refused the same record.
//
// Three readers of one rule is how the drift happened, so there is one
// validator now and this test holds all three paths against it.
func TestABlankDomainRecordDoesNotDisableContinuity(t *testing.T) {
	current := valid(t)
	applied, err := solutionhost.AppliedFrom(current)
	require.NoError(t, err)
	blank := applied
	blank.Domain = ""

	moved := valid(t)
	moved.Generation = current.Generation + 1
	moved.OwnershipDomain = "beta"

	// The rendered fold.
	admissions, err := solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{
		Document: moved, Applied: blank,
	})
	require.ErrorIs(t, err, solutionhost.ErrAppliedUnusable,
		"a record with no domain is hand-built, not a record with a weaker rule")
	require.Empty(t, admissions[0].Decision)

	// Activation.
	request := activationOf(t, validAuthority(t), valid(t), valid(t).Workloads[0].Image.Digest)
	request.Records = recordsHolding(blank)
	_, err = solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrAppliedUnusable)

	// And a record WITH a domain still catches the move, which is the rule
	// the blank one was disabling.
	admissions, err = solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{
		Document: moved, Applied: applied,
	})
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)
	require.Empty(t, admissions[0].Decision)
}
