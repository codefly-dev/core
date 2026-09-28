package solutionhost_test

import (
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
// Changing either constant is therefore a deliberate act with a migration
// behind it, never a side effect of an edit.
const (
	validFixtureDigest     = "sha256:01691985abe49c65797b7d428df6936b64554575318808860c995bce106feb14"
	tombstoneFixtureDigest = "sha256:a1c101ba873c235004c25dc18a09e9fffa694b7d5ef7e0773ac86e24b5b26a7d"
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
	require.Contains(t, string(canonical), `"host":{"component":"saas-host","coordinate":`)
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
// "/" and "@", so a "/" inside either would let publisher "obin" + name
// "crm/web" and publisher "obin/crm" + name "web" render the same identity —
// and every artifact references its release by exactly that string.
func TestReleaseIdentityCannotBeAmbiguous(t *testing.T) {
	for name, release := range map[string]solutionhost.Release{
		"slash in publisher": {Publisher: "obin/crm", Name: "web", Version: "1.0.0"},
		"slash in name":      {Publisher: "obin", Name: "crm/web", Version: "1.0.0"},
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
		solutionhost.Release{Publisher: "obin", Name: "crm", Version: "1.0.0"}.Identity(),
		solutionhost.Release{Publisher: "obin", Name: "crm-web", Version: "1.0.0"}.Identity())
}

// Route aliases are unique within ONE host. A product delivery repo covers
// every host it deploys to, so the renderer's set legitimately carries the same
// alias on two coordinates — that is one solution in two regions, not a
// collision.
func TestTheSameAliasOnTwoCoordinatesIsNotACollision(t *testing.T) {
	eu := parse(t, "valid")
	us := parse(t, "valid")
	us.Binding = "crm-us-east-1-01"
	us.Host.Coordinate = "obin/prod/us-east-1"
	require.Equal(t, eu.Aliases(), us.Aliases())

	admissions, err := solutionhost.Host{}.Admit(eu, us)
	require.NoError(t, err)
	require.Equal(t, []solutionhost.Admission{
		{Binding: eu.Binding, Decision: solutionhost.DecisionApply},
		{Binding: us.Binding, Decision: solutionhost.DecisionApply},
	}, admissions)

	// The same alias twice on ONE coordinate still collides.
	us.Host.Coordinate = eu.Host.Coordinate
	_, err = solutionhost.Host{}.Admit(eu, us)
	require.ErrorIs(t, err, composition.ErrCollision)
}

// Applied state is one host's durable record and names no coordinate of its
// own, so it is only interpretable against a named host.
func TestAppliedStateRequiresANamedHost(t *testing.T) {
	applied, err := solutionhost.AppliedFrom(parse(t, "valid"))
	require.NoError(t, err)

	_, err = solutionhost.Host{Applied: []solutionhost.Applied{applied}}.Admit(parse(t, "valid"))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "Host.Coordinate is required")
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
		Coordinate: solutionhost.FixtureCoordinate,
		Reserved:   []string{"codefly"},
		Applied:    []solutionhost.Applied{applied},
	}

	fresh := parse(t, "valid")
	fresh.Binding = "crm-02"
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

// Validity is a per-document property, so one malformed binding must not freeze
// every other binding on the host. The returned error still fails a caller that
// checks only it.
func TestOneMalformedDocumentDoesNotRefuseTheRest(t *testing.T) {
	good := parse(t, "valid")
	bad := parse(t, "valid")
	bad.Binding = "typo-01"
	bad.Routes = nil
	bad.Generation = 0

	admissions, err := solutionhost.Host{}.Admit(good, bad)
	require.Error(t, err)
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Len(t, admissions, 2)

	require.NoError(t, admissions[0].Err)
	require.Equal(t, good.Binding, admissions[0].Binding)
	require.Equal(t, solutionhost.DecisionApply, admissions[0].Decision)

	require.ErrorIs(t, admissions[1].Err, solutionhost.ErrInvalid)
	require.Empty(t, admissions[1].Decision)
	// The document never validated, so it never named a binding.
	require.Empty(t, admissions[1].Binding)
}

// A redeclaration refused before the alias pass — here for being an older
// generation — never releases its applied alias either.
func TestARedeclarationRefusedForItsGenerationKeepsItsAppliedAlias(t *testing.T) {
	host := fixtureHost(t)
	require.Equal(t, []string{"crm"}, host.Applied[0].Routes)

	// crm-eu-west-1-01 looks like it is releasing "crm"… but the document is an
	// older generation and will be refused, so the applied one still holds it.
	stale := parse(t, "stale-generation")
	stale.Routes = nil
	stale.Artifacts = stale.Artifacts[:1]
	require.Equal(t, host.Applied[0].Binding, stale.Binding)

	claimant := parse(t, "duplicate-route-alias")

	admissions, err := host.Admit(stale, claimant)
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
// applied generation as replaced and "crm" as released — which lets bbb-01
// take it. Only when aaa-01 is then refused for claiming "zzz" does it become
// true that aaa-01 still holds "crm", and bbb-01 must be refused too. A
// single-pass implementation admits bbb-01 here and collides at apply time.
func TestTheAliasPassRerunsAfterARefusalFreesNothing(t *testing.T) {
	incumbent := parse(t, "valid")
	incumbent.Binding = "aaa-01"
	incumbentApplied, err := solutionhost.AppliedFrom(incumbent)
	require.NoError(t, err)
	require.Equal(t, []string{"crm"}, incumbentApplied.Routes)

	neighbour := parse(t, "valid")
	neighbour.Binding = "zzz-01"
	neighbour.Routes = []solutionhost.Route{{Alias: "zzz", Surface: solutionhost.SurfaceFrontend}}
	neighbourApplied, err := solutionhost.AppliedFrom(neighbour)
	require.NoError(t, err)

	host := solutionhost.Host{
		Coordinate: solutionhost.FixtureCoordinate,
		Applied:    []solutionhost.Applied{incumbentApplied, neighbourApplied},
	}

	// aaa-01 moves off "crm" and onto an alias zzz-01 already holds.
	moving := parse(t, "valid")
	moving.Binding = "aaa-01"
	moving.Generation = incumbent.Generation + 1
	moving.Routes = []solutionhost.Route{{Alias: "zzz", Surface: solutionhost.SurfaceFrontend}}

	// bbb-01 wants the alias aaa-01 appeared to be releasing.
	successor := parse(t, "valid")
	successor.Binding = "bbb-01"
	successor.Generation = 1

	admissions, err := host.Admit(moving, successor)
	require.Error(t, err)
	require.Len(t, admissions, 2)
	require.ErrorIs(t, admissions[0].Err, composition.ErrCollision)
	require.ErrorIs(t, admissions[1].Err, composition.ErrCollision,
		"aaa-01 was refused, so it never released \"crm\" and bbb-01 cannot have it")
}
