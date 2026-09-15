package composition

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/require"

	coreversion "github.com/codefly-dev/core/version"
)

func fixtureManifest(id string, fixtures ...ProvidedFixture) *PackageManifest {
	return &PackageManifest{
		Kind: PackageKind, Schema: PackageSchema, ID: id, Version: "0.1.0",
		MinimumCodeflyVersion: ">=0.3.32", ArtifactRoots: []string{"services"},
		Contracts: map[string]string{ContractComposition: ">=2.0 <3.0", ContractFixtures: ">=1.0 <2.0"},
		Fixtures:  fixtures,
	}
}

func devAdminFixture() ProvidedFixture {
	return ProvidedFixture{
		Name:        "dev-admin",
		Description: "Seeded tenant with an administrator",
		Principals: []FixturePrincipal{
			{ID: "dev-admin", Email: "admin@dev.local", Role: "super_admin", Token: "dev-admin-provider-id"},
			{ID: "dev-member", Email: "member@dev.local", Role: "member", Token: "dev-member-provider-id"},
		},
	}
}

func TestPackageManifestCarriesFixtures(t *testing.T) {
	manifest := mustManifest(t, newPackageRoot(t, "0.1.0"))
	require.Len(t, manifest.Fixtures, 1)
	require.Equal(t, "dev-admin", manifest.Fixtures[0].Name)
	require.Equal(t, "Seeded tenant with an administrator", manifest.Fixtures[0].Description)
	require.Equal(t, []FixturePrincipal{
		{ID: "dev-admin", Email: "admin@dev.local", Role: "super_admin", Token: "dev-admin-provider-id"},
	}, manifest.Fixtures[0].Principals)
}

func TestFixtureDeclarationsAreValidated(t *testing.T) {
	require.NoError(t, fixtureManifest(testPackage, devAdminFixture()).Validate())

	duplicateRole := devAdminFixture()
	duplicateRole.Principals[1].Role = "super_admin"
	incomplete := devAdminFixture()
	incomplete.Principals[0].Token = ""
	duplicateID := devAdminFixture()
	duplicateID.Principals[1].ID = "dev-admin"
	untrimmedRole := devAdminFixture()
	untrimmedRole.Principals[0].Role = " super_admin"
	undeclaredContract := fixtureManifest(testPackage, devAdminFixture())
	delete(undeclaredContract.Contracts, ContractFixtures)
	staleMinimum := fixtureManifest(testPackage, devAdminFixture())
	staleMinimum.MinimumCodeflyVersion = ">=0.1.0"

	for name, test := range map[string]struct {
		manifest *PackageManifest
		message  string
	}{
		"duplicate fixture":   {fixtureManifest(testPackage, devAdminFixture(), devAdminFixture()), `duplicate fixture "dev-admin"`},
		"duplicate role":      {fixtureManifest(testPackage, duplicateRole), `duplicate principal role in fixture dev-admin "super_admin"`},
		"duplicate principal": {fixtureManifest(testPackage, duplicateID), `duplicate principal in fixture dev-admin "dev-admin"`},
		"incomplete":          {fixtureManifest(testPackage, incomplete), "fixture dev-admin principal 0 requires an id, an email, a role, and a token"},
		"untrimmed role":      {fixtureManifest(testPackage, untrimmedRole), "without surrounding whitespace"},
		"invalid name":        {fixtureManifest(testPackage, ProvidedFixture{Name: "Dev Admin"}), `fixture name "Dev Admin" is invalid`},
		"name with slash":     {fixtureManifest(testPackage, ProvidedFixture{Name: "dev/admin"}), `fixture name "dev/admin" cannot contain "/"`},
		"undeclared contract": {undeclaredContract, `must declare the "fixtures" contract`},
		"stale minimum":       {staleMinimum, "requires Codefly newer than " + LastCodeflyVersionWithoutFixtures},
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, test.manifest.Validate(), test.message)
		})
	}
}

// A fixture principal renamed in a package release must fail verification of
// that release rather than surfacing as a login failure in a downstream test.
func TestReleaseVerificationCoversFixtures(t *testing.T) {
	root := newPackageRoot(t, "0.1.0")
	manifestPath := filepath.Join(root, PackageManifestFileName)
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, bytes.Replace(data, []byte("        token: dev-admin-provider-id\n"), nil, 1), 0o644))

	release, trust := buildRelease(t, root, "0.1.0", strings.Repeat("a", 40))
	_, err = VerifyRelease(release, testPackage, "0.1.0", trust)
	require.ErrorContains(t, err, "requires an id, an email, a role, and a token")
}

func TestResolveFixtureNamesTheAvailableFixtures(t *testing.T) {
	packages := []*PackageManifest{
		fixtureManifest(testPackage, devAdminFixture()),
		fixtureManifest("codefly/billing", ProvidedFixture{Name: "simple"}),
	}

	fixtures, err := Fixtures(packages...)
	require.NoError(t, err)
	require.Equal(t, []string{"dev-admin", "simple"}, []string{fixtures[0].Name, fixtures[1].Name})

	resolved, err := ResolveFixture("dev-admin", packages...)
	require.NoError(t, err)
	require.Equal(t, "dev-admin", resolved.Name)

	_, err = ResolveFixture("dev-admn", packages...)
	require.ErrorIs(t, err, ErrUnknownFixture)
	require.ErrorContains(t, err, "available: dev-admin, simple")

	_, err = ResolveFixture("dev-admin")
	require.ErrorIs(t, err, ErrUnknownFixture)
	require.ErrorContains(t, err, "declare no fixtures")

	_, err = Fixtures(packages[0], fixtureManifest("codefly/other", devAdminFixture()))
	require.ErrorIs(t, err, ErrCollision)
	require.ErrorContains(t, err, `declared by both "codefly/saas-starter" and "codefly/other"`)

	// A lost package must not resolve to a fixture set that silently omits
	// whatever it declared, nor take down the caller.
	_, err = Fixtures(packages[0], nil)
	require.ErrorContains(t, err, "module package manifest is required")
	_, err = ResolveFixture("dev-admin", nil)
	require.ErrorContains(t, err, "module package manifest is required")
}

func TestFixturePrincipalsResolveByRole(t *testing.T) {
	fixture := devAdminFixture()

	principal, err := fixture.Principal("super_admin")
	require.NoError(t, err)
	require.Equal(t, "dev-admin", principal.ID)
	require.Equal(t, "admin@dev.local", principal.Email)
	require.Equal(t, "dev-admin-provider-id", principal.Token)

	_, err = fixture.Principal("owner")
	require.ErrorIs(t, err, ErrUnknownPrincipal)
	require.ErrorContains(t, err, "seeded roles: super_admin, member")

	_, err = (&ProvidedFixture{Name: "simple"}).Principal("super_admin")
	require.ErrorIs(t, err, ErrUnknownPrincipal)
	require.ErrorContains(t, err, "seeds no principals")
}

// A package's fixtures bind every consumer of it, so the fixtures contract must
// be negotiated and locked even when the consumer contributes no fixtures of
// its own — otherwise nothing pins the version a breaking bump would change.
func TestFixturesAreLockedByEveryConsumerOfThePackage(t *testing.T) {
	descriptor := &Descriptor{Kind: DescriptorKind, Name: "saas", Base: Base{ID: testPackage, Version: "^0.1"}}
	require.Empty(t, descriptor.Contributions.Fixtures)
	manifest := fixtureManifest(testPackage, devAdminFixture())

	negotiated, err := NegotiateContracts(descriptor, manifest, "0.3.32", DefaultSupportedContracts)
	require.NoError(t, err)
	require.Equal(t, "1.0", negotiated[ContractFixtures])

	lock := validLock()
	require.NotContains(t, lock.Contracts, ContractFixtures)
	err = ValidateLockedContracts(descriptor, manifest, lock, "0.3.32", DefaultSupportedContracts)
	require.ErrorIs(t, err, ErrContract)
	require.ErrorContains(t, err, `lock is missing required contract "fixtures"`)

	lock.Contracts[ContractFixtures] = negotiated[ContractFixtures]
	require.NoError(t, ValidateLockedContracts(descriptor, manifest, lock, "0.3.32", DefaultSupportedContracts))
}

func TestResolvedFixtureIsDecoupledFromTheManifest(t *testing.T) {
	manifest := fixtureManifest(testPackage, devAdminFixture())

	resolved, err := ResolveFixture("dev-admin", manifest)
	require.NoError(t, err)
	resolved.Principals[0].Token = "rewritten"

	require.Equal(t, "dev-admin-provider-id", manifest.Fixtures[0].Principals[0].Token)
}

// The gate only holds while the constant names a version that has actually been
// released; a constant ahead of the build would reject every honest package.
func TestFixtureGateIsNotAheadOfTheShippedVersion(t *testing.T) {
	shipped, err := coreversion.Version(context.Background())
	require.NoError(t, err)
	gate, err := semver.NewVersion(LastCodeflyVersionWithoutFixtures)
	require.NoError(t, err)
	current, err := semver.NewVersion(shipped)
	require.NoError(t, err)
	require.False(t, current.LessThan(gate), "shipped version %s is older than the fixtures gate %s", shipped, gate)
}

func TestSemanticReportShowsFixtureChanges(t *testing.T) {
	before := validLock()
	afterValue := *before
	afterValue.Version = "0.2.0"

	renamed := devAdminFixture()
	renamed.Principals[0].Role = "owner"
	report := newSemanticReport(
		&Descriptor{Name: "saas"}, before, &afterValue,
		fixtureManifest(testPackage, devAdminFixture(), ProvidedFixture{Name: "simple"}),
		fixtureManifest(testPackage, renamed),
		nil, nil, nil,
	)
	require.Equal(t, []string{"dev-admin/owner"}, report.Fixtures.Added)
	require.Equal(t, []string{"dev-admin/super_admin", "simple"}, report.Fixtures.Removed)
	require.Contains(t, report.String(), "fixtures")
}
