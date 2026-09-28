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

func parse(t *testing.T, name string) *solutionhost.SolutionHostBinding {
	t.Helper()
	document, err := solutionhost.Parse(fixture(t, name))
	require.NoError(t, err)
	return document
}

// The whole conformance kit, driven the way codefly-dev/cli and the host in
// codefly-dev/module-saas-starter are expected to drive it.
func TestShippedFixturesReachTheirDeclaredOutcome(t *testing.T) {
	host := fixtureHost(t)
	fixtures := solutionhost.Fixtures()
	require.Len(t, fixtures, 5)

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
			decisions, err := host.Admit(document)
			if shipped.Outcome == solutionhost.OutcomeRejected {
				require.Error(t, err)
				require.Nil(t, decisions)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []solutionhost.Decision{shipped.Decision}, decisions)
		})
	}
}

func TestEachRejectedFixtureNamesWhyItWasRejected(t *testing.T) {
	host := fixtureHost(t)
	for name, target := range map[string]error{
		"stale-generation":      solutionhost.ErrStaleGeneration,
		"duplicate-route-alias": composition.ErrCollision,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := host.Admit(parse(t, name))
			require.ErrorIs(t, err, target)
		})
	}
	_, err := solutionhost.Parse(fixture(t, "mixed-release"))
	require.ErrorIs(t, err, solutionhost.ErrMixedRelease)
}

func TestARereadOfTheAppliedGenerationIsCurrentAndARewriteIsAnError(t *testing.T) {
	host := fixtureHost(t)

	decisions, err := host.Admit(parse(t, "valid"))
	require.NoError(t, err)
	require.Equal(t, []solutionhost.Decision{solutionhost.DecisionCurrent}, decisions)

	rewritten := parse(t, "valid")
	rewritten.Artifacts[0].Digest = "sha256:" + strings.Repeat("0", 64)
	_, err = host.Admit(rewritten)
	require.ErrorIs(t, err, solutionhost.ErrRewrittenGeneration)
}

func TestATombstoneReleasesItsAliasForANewInstance(t *testing.T) {
	host := fixtureHost(t)

	tombstone := parse(t, "tombstone")
	decisions, err := host.Admit(tombstone)
	require.NoError(t, err)
	require.Equal(t, []solutionhost.Decision{solutionhost.DecisionApply}, decisions)

	applied, err := solutionhost.AppliedFrom(tombstone)
	require.NoError(t, err)
	require.True(t, applied.Removed)
	require.Empty(t, applied.Routes)

	// Once the tombstone is the applied generation, the alias is free — but the
	// binding ID is not: its generation history survives removal.
	removed := solutionhost.Host{Coordinate: solutionhost.FixtureCoordinate, Applied: []solutionhost.Applied{applied}}
	_, err = removed.Admit(parse(t, "duplicate-route-alias"))
	require.NoError(t, err)
	_, err = removed.Admit(parse(t, "valid"))
	require.ErrorIs(t, err, solutionhost.ErrStaleGeneration)
}

func TestABindingMovingItsOwnAliasDoesNotCollideWithItself(t *testing.T) {
	host := fixtureHost(t)

	next := parse(t, "valid")
	next.Generation = 5
	next.Routes = []solutionhost.Route{{Alias: "crm/v2", Surface: solutionhost.SurfaceFrontend}}

	decisions, err := host.Admit(next)
	require.NoError(t, err)
	require.Equal(t, []solutionhost.Decision{solutionhost.DecisionApply}, decisions)
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
	decisions, err := solutionhost.Host{}.Admit(first, second)
	require.NoError(t, err)
	require.Equal(t, []solutionhost.Decision{solutionhost.DecisionApply, solutionhost.DecisionApply}, decisions)

	// An empty set is "nothing declared", not "remove everything": removal is a
	// generation, so Admit has nothing to say about it.
	decisions, err = solutionhost.Host{}.Admit()
	require.NoError(t, err)
	require.Empty(t, decisions)
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

	_, err := fixtureHost(t).Admit(document)
	require.ErrorIs(t, err, solutionhost.ErrWrongHost)
	require.Contains(t, err.Error(), "us-east-1")
}

func TestAHostReservesRouteNamespaces(t *testing.T) {
	host := solutionhost.Host{Coordinate: solutionhost.FixtureCoordinate, Reserved: []string{"codefly"}}

	document := parse(t, "valid")
	document.Routes = []solutionhost.Route{{Alias: "codefly/admin", Surface: solutionhost.SurfaceFrontend}}
	_, err := host.Admit(document)
	require.ErrorIs(t, err, composition.ErrCollision)

	document.Routes = []solutionhost.Route{{Alias: "codeflyer", Surface: solutionhost.SurfaceFrontend}}
	_, err = host.Admit(document)
	require.NoError(t, err)
}

func TestInvalidAppliedStateIsRejectedRatherThanTrusted(t *testing.T) {
	document := parse(t, "valid")
	digest, err := document.Digest()
	require.NoError(t, err)
	sound := solutionhost.Applied{Binding: document.Binding, Generation: 4, Digest: digest, Routes: []string{"crm"}}

	for name, applied := range map[string][]solutionhost.Applied{
		"no binding ID":   {{Generation: 1, Digest: digest}},
		"generation zero": {{Binding: "other-01", Generation: 0, Digest: digest}},
		"no digest":       {{Binding: "other-01", Generation: 1}},
		"recorded twice":  {sound, sound},
		"tombstone holding a route": {{
			Binding: "other-01", Generation: 1, Digest: digest, Routes: []string{"other"}, Removed: true,
		}},
		"two bindings holding one alias": {sound, {
			Binding: "other-01", Generation: 1, Digest: digest, Routes: []string{"crm"},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := solutionhost.Host{Applied: applied}.Admit(document)
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
		Binding: document.Binding, Generation: 4, Digest: digest, Routes: []string{"crm"},
	}, applied)

	document.Generation = 0
	_, err = solutionhost.AppliedFrom(document)
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
}
