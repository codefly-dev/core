package composition

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Fixtures returns the fixtures the composed packages declare, sorted by name.
// Two packages declaring the same name collide: a fixture selection would no
// longer name one seed.
func Fixtures(manifests ...*PackageManifest) ([]ProvidedFixture, error) {
	var fixtures []ProvidedFixture
	owners := make(map[string]string)
	for _, manifest := range manifests {
		// A nil entry means the caller lost a composed package: resolving from
		// the rest would answer with a fixture set that is missing whatever that
		// package declared, and miss a name collision against it.
		if manifest == nil {
			return nil, errors.New("module package manifest is required")
		}
		for _, fixture := range manifest.Fixtures {
			if previous, exists := owners[fixture.Name]; exists {
				return nil, fmt.Errorf("%w: fixture %q is declared by both %q and %q", ErrCollision, fixture.Name, previous, manifest.ID)
			}
			owners[fixture.Name] = manifest.ID
			// The struct copy still shares the principals array with the
			// manifest, so without this a caller editing a resolved principal
			// would rewrite the package's own declaration.
			fixture.Principals = slices.Clone(fixture.Principals)
			fixtures = append(fixtures, fixture)
		}
	}
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].Name < fixtures[j].Name })
	return fixtures, nil
}

// ResolveFixture returns the fixture the composed packages declare under name.
// The error names the available fixtures, so a typo fails at load rather than
// booting a host with no seed.
func ResolveFixture(name string, manifests ...*PackageManifest) (*ProvidedFixture, error) {
	fixtures, err := Fixtures(manifests...)
	if err != nil {
		return nil, err
	}
	available := make([]string, 0, len(fixtures))
	for _, fixture := range fixtures {
		if fixture.Name == name {
			return &fixture, nil
		}
		available = append(available, fixture.Name)
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("%w: %q, and the composed packages declare no fixtures", ErrUnknownFixture, name)
	}
	return nil, fmt.Errorf("%w: %q (available: %s)", ErrUnknownFixture, name, strings.Join(available, ", "))
}

// Principal returns the identity the fixture seeds for role. Tests resolve by
// role so that a renamed or dropped principal fails here, against the package
// version they composed, instead of at login.
func (fixture *ProvidedFixture) Principal(role string) (*FixturePrincipal, error) {
	seeded := make([]string, 0, len(fixture.Principals))
	for _, principal := range fixture.Principals {
		if principal.Role == role {
			return &principal, nil
		}
		seeded = append(seeded, principal.Role)
	}
	if len(seeded) == 0 {
		return nil, fmt.Errorf("%w: fixture %q seeds no principals", ErrUnknownPrincipal, fixture.Name)
	}
	return nil, fmt.Errorf("%w: fixture %q has no %q principal (seeded roles: %s)", ErrUnknownPrincipal, fixture.Name, role, strings.Join(seeded, ", "))
}
