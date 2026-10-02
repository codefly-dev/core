package solutionhost_test

import (
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

// admitOne drives the single-document case every host-side test uses and
// asserts the Admission agrees with the returned error.
func admitOne(t *testing.T, host solutionhost.Host, document *solutionhost.SolutionHostBinding) (solutionhost.Decision, error) {
	t.Helper()
	admissions, err := host.Admit(document)
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
		Coordinate: solutionhost.FixtureCoordinate,
		Domains:    []string{solutionhost.FixtureDomain},
		Applied:    applied,
	}
}

// The whole conformance kit, driven the way codefly-dev/cli and the host in
// codefly-dev/module-saas-starter are expected to drive it.
func TestShippedFixturesReachTheirDeclaredOutcome(t *testing.T) {
	host := fixtureHost(t)
	fixtures := solutionhost.FixturesOf(solutionhost.DocumentTypePresence)
	require.Len(t, fixtures, 11)

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
		"mixed-release":     solutionhost.ErrMixedRelease,
		"wrong-kind":        solutionhost.ErrInvalid,
		"missing-identity":  solutionhost.ErrInvalid,
		"digest-confusion":  solutionhost.ErrDigestConfusion,
		"superseded-schema": solutionhost.ErrSchema,
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
	next.Routes = []solutionhost.Route{{Alias: "crm/v2", Surface: solutionhost.SurfaceFrontend}}

	decision, err := admitOne(t, host, next)
	require.NoError(t, err)
	require.Equal(t, solutionhost.DecisionApply, decision)
}

func TestARendererChecksASetBeforeItIsDelivered(t *testing.T) {
	first := parse(t, "valid")
	second := parse(t, "duplicate-route-alias")

	// The zero Host is the renderer's view: nothing applied, no coordinate
	// pinned, and the collision still refused where the set was authored.
	_, err := solutionhost.Host{}.Admit(first, second)
	require.ErrorIs(t, err, composition.ErrCollision)
	require.Contains(t, err.Error(), first.Binding)
	require.Contains(t, err.Error(), second.Binding)

	second.Routes[0].Alias = "crm2"
	admissions, err := solutionhost.Host{}.Admit(first, second)
	require.NoError(t, err)
	require.Equal(t, []solutionhost.Admission{
		{Binding: first.Binding, Decision: solutionhost.DecisionApply},
		{Binding: second.Binding, Decision: solutionhost.DecisionApply},
	}, admissions)

	// An empty set is "nothing declared", not "remove everything": removal is a
	// generation, so Admit has nothing to say about it.
	admissions, err = solutionhost.Host{}.Admit()
	require.NoError(t, err)
	require.Empty(t, admissions)
}

func TestOneSetDeclaresOneGenerationPerBinding(t *testing.T) {
	first := parse(t, "valid")
	second := parse(t, "valid")
	second.Generation = 6
	second.Routes = nil

	_, err := solutionhost.Host{}.Admit(first, second)
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "declared twice")
}

func TestADocumentForAnotherHostIsRefused(t *testing.T) {
	document := parse(t, "valid")
	document.Host.Coordinate = "obin/prod/us-east-1"

	_, err := admitOne(t, fixtureHost(t), document)
	require.ErrorIs(t, err, solutionhost.ErrWrongHost)
	require.Contains(t, err.Error(), "us-east-1")
}

func TestAHostReservesRouteNamespaces(t *testing.T) {
	host := solutionhost.Host{
		Coordinate: solutionhost.FixtureCoordinate,
		Domains:    []string{solutionhost.FixtureDomain},
		Reserved:   []string{"codefly"},
	}

	document := parse(t, "valid")
	document.Routes = []solutionhost.Route{{Alias: "codefly/admin", Surface: solutionhost.SurfaceFrontend}}
	_, err := admitOne(t, host, document)
	require.ErrorIs(t, err, composition.ErrCollision)

	document.Routes = []solutionhost.Route{{Alias: "codeflyer", Surface: solutionhost.SurfaceFrontend}}
	_, err = admitOne(t, host, document)
	require.NoError(t, err)
}

func TestInvalidAppliedStateIsRejectedRatherThanTrusted(t *testing.T) {
	document := parse(t, "valid")
	digest, err := document.Digest()
	require.NoError(t, err)
	domain := solutionhost.FixtureDomain
	sound := solutionhost.Applied{
		Binding: document.Binding, Generation: 4, Digest: digest, Domain: domain, Routes: []string{"crm"},
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
		"two bindings holding one alias": {sound, {
			Binding: "other-01", Generation: 1, Digest: digest, Domain: domain, Routes: []string{"crm"},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := appliedHost(applied...).Admit(document)
			require.Error(t, err)
		})
	}
}

func TestAppliedFromRecordsWhatTheHostMustPersist(t *testing.T) {
	document := parse(t, "valid")
	applied, err := solutionhost.AppliedFrom(document)
	require.NoError(t, err)

	digest, err := document.Digest()
	require.NoError(t, err)
	require.Equal(t, solutionhost.Applied{
		Binding: document.Binding, Generation: 4, Digest: digest,
		Domain: solutionhost.FixtureDomain, Routes: []string{"crm"},
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

	second.OwnershipDomain = "pim"
	err := solutionhost.OneDelivery(first, second)
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)
	require.Contains(t, err.Error(), "pim")
	require.Contains(t, err.Error(), solutionhost.FixtureDomain)

	// A document that does not validate is its own refusal and never decides
	// the set's: otherwise one malformed document would withhold a delivery.
	malformed := parse(t, "valid")
	malformed.OwnershipDomain = "not a domain"
	require.NoError(t, solutionhost.OneDelivery(first, malformed))
}

// A host's mount is the union of however many deliveries reached it, so it
// legitimately carries one domain per delivery. Admit must not refuse that —
// which is the whole reason OneDelivery is a separate call — and ByDomain is
// how a host turns the union back into one answerable question per delivery.
func TestAHostAdmitsAMountHoldingSeveralDeliveries(t *testing.T) {
	fromCRM := parse(t, "valid")
	fromPIM := parse(t, "module-presence")
	fromPIM.OwnershipDomain = "pim"
	fromPIM.Routes = nil

	host := solutionhost.Host{
		Coordinate: solutionhost.FixtureCoordinate,
		Domains:    []string{solutionhost.FixtureDomain, "pim"},
		Applied:    fixtureHost(t).Applied,
	}
	admissions, err := host.Admit(fromCRM, fromPIM)
	require.NoError(t, err, "two deliveries into one mount is the normal case, not a straddle")
	require.Equal(t, solutionhost.DecisionCurrent, admissions[0].Decision)
	require.Equal(t, solutionhost.DecisionApply, admissions[1].Decision)

	grouped := solutionhost.ByDomain(fromCRM, fromPIM)
	require.Len(t, grouped, 2)
	require.Equal(t, []*solutionhost.SolutionHostBinding{fromCRM}, grouped[solutionhost.FixtureDomain])
	require.Equal(t, []*solutionhost.SolutionHostBinding{fromPIM}, grouped["pim"])

	// A domain the host does not accept is refused per document, which is the
	// rule that bounds a binding's first generation.
	narrow := solutionhost.Host{
		Coordinate: solutionhost.FixtureCoordinate,
		Domains:    []string{solutionhost.FixtureDomain},
		Applied:    fixtureHost(t).Applied,
	}
	_, err = admitOne(t, narrow, fromPIM)
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)
	require.Contains(t, err.Error(), "does not accept")
}

// A named host must say which domains it accepts. An unstated list would accept
// every domain, which is the hole the field exists to close — so it is an error
// rather than a permissive default. A renderer leaves both empty.
func TestANamedHostMustDeclareTheDomainsItAccepts(t *testing.T) {
	_, err := solutionhost.Host{Coordinate: solutionhost.FixtureCoordinate}.Admit(parse(t, "valid"))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "ownership domains it accepts")

	_, err = solutionhost.Host{
		Coordinate: solutionhost.FixtureCoordinate,
		Domains:    []string{"not a domain"},
	}.Admit(parse(t, "valid"))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)

	// The renderer's view, unaffected: no coordinate, no domains, and every
	// check that does not need host state still runs.
	_, err = solutionhost.Host{}.Admit(parse(t, "valid"))
	require.NoError(t, err)
}

// A binding keeps the domain it was applied under, and a tombstone is the case
// that matters: otherwise any delivery the host accepts could withdraw any
// binding by declaring a higher generation under its own domain.
func TestABindingKeepsTheDomainItWasAppliedUnder(t *testing.T) {
	host := solutionhost.Host{
		Coordinate: solutionhost.FixtureCoordinate,
		Domains:    []string{solutionhost.FixtureDomain, "pim"},
		Applied:    fixtureHost(t).Applied,
	}

	// The host accepts "pim", so this is refused on ownership and not on
	// acceptance — which is what makes the two rules distinct.
	foreign := parse(t, "tombstone-foreign-domain")
	require.Equal(t, "pim", foreign.OwnershipDomain)
	require.Greater(t, foreign.Generation, host.Applied[0].Generation)

	_, err := admitOne(t, host, foreign)
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)
	require.Contains(t, err.Error(), "was applied under domain")

	// The same withdrawal from the delivery that owns the binding applies.
	_, err = admitOne(t, host, parse(t, "tombstone"))
	require.NoError(t, err)
}
