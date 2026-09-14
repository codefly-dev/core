package composition

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixtureManifest(id string, fixtures ...ProvidedFixture) *PackageManifest {
	return &PackageManifest{
		Kind: PackageKind, Schema: PackageSchema, ID: id, Version: "0.1.0",
		MinimumCodeflyVersion: ">=0.1.0", ArtifactRoots: []string{"services"},
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
	undeclaredContract := fixtureManifest(testPackage, devAdminFixture())
	delete(undeclaredContract.Contracts, ContractFixtures)

	for name, test := range map[string]struct {
		manifest *PackageManifest
		message  string
	}{
		"duplicate fixture":   {fixtureManifest(testPackage, devAdminFixture(), devAdminFixture()), `duplicate fixture "dev-admin"`},
		"duplicate role":      {fixtureManifest(testPackage, duplicateRole), `duplicate principal role in fixture dev-admin "super_admin"`},
		"duplicate principal": {fixtureManifest(testPackage, duplicateID), `duplicate principal in fixture dev-admin "dev-admin"`},
		"incomplete":          {fixtureManifest(testPackage, incomplete), "fixture dev-admin principal 0 requires an id, an email, a role, and a token"},
		"invalid name":        {fixtureManifest(testPackage, ProvidedFixture{Name: "Dev Admin"}), `fixture name "Dev Admin" is invalid`},
		"undeclared contract": {undeclaredContract, `must declare the "fixtures" contract`},
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
