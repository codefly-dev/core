package composition

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const catalogSchema = "codefly/composition-catalog/v2"

func ValidateCollisions(claims []Claim, reservedNamespaces []string) error {
	ordered := append([]Claim(nil), claims...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Kind != ordered[j].Kind {
			return ordered[i].Kind < ordered[j].Kind
		}
		if ordered[i].Key != ordered[j].Key {
			return ordered[i].Key < ordered[j].Key
		}
		return ordered[i].Owner < ordered[j].Owner
	})
	seen := make(map[string]Claim, len(ordered))
	for _, claim := range ordered {
		if err := claim.validate(); err != nil {
			return err
		}
		for _, namespace := range reservedNamespaces {
			if claim.Owner != "base" && inNamespace(claim.Key, namespace) {
				return fmt.Errorf("%w: %s %q owned by %q uses reserved namespace %q", ErrCollision, claim.Kind, claim.Key, claim.Owner, namespace)
			}
		}
		identity := string(claim.Kind) + "\x00" + claim.Key
		if previous, exists := seen[identity]; exists {
			return fmt.Errorf("%w: %s %q is claimed by both %q and %q", ErrCollision, claim.Kind, claim.Key, previous.Owner, claim.Owner)
		}
		seen[identity] = claim
	}
	return nil
}

func inNamespace(key, namespace string) bool {
	if namespace == "" {
		return false
	}
	return key == namespace || strings.HasPrefix(key, namespace+"/") || strings.HasPrefix(key, namespace+".") || strings.HasPrefix(key, namespace+":")
}

func LoadCatalog(projection string) (*Catalog, error) {
	data, err := os.ReadFile(filepath.Join(projection, filepath.FromSlash(CompositionCatalogName)))
	if err != nil {
		return nil, fmt.Errorf("read composition catalog: %w", err)
	}
	var catalog Catalog
	if err := decodeStrictJSON(data, &catalog); err != nil {
		return nil, fmt.Errorf("decode composition catalog: %w", err)
	}
	if catalog.Schema != catalogSchema {
		return nil, fmt.Errorf("composition catalog schema must be %q", catalogSchema)
	}
	for _, claim := range catalog.Claims {
		if err := claim.validate(); err != nil {
			return nil, err
		}
	}
	if err := validateCatalogInputs(catalog.Inputs); err != nil {
		return nil, err
	}
	if err := uniqueStrings("composition dependency", catalog.Dependencies); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func ValidateCatalog(catalog *Catalog, expected []CatalogInput) error {
	if catalog == nil {
		return errors.New("composition catalog is required")
	}
	if err := validateCatalogInputs(catalog.Inputs); err != nil {
		return err
	}
	actualByPath := make(map[string]CatalogInput, len(catalog.Inputs))
	for _, input := range catalog.Inputs {
		actualByPath[input.Kind+"\x00"+input.Path] = input
	}
	expectedOwners := make(map[string]struct{}, len(expected))
	for _, input := range expected {
		actual, exists := actualByPath[input.Kind+"\x00"+input.Path]
		if !exists || actual.Identity != input.Identity || actual.Digest != input.Digest {
			return fmt.Errorf("composition catalog did not validate %s contribution %q", input.Kind, input.Path)
		}
		expectedOwners[input.Path] = struct{}{}
	}
	if len(actualByPath) != len(expected) {
		return errors.New("composition catalog contains undeclared contribution inputs")
	}
	for _, claim := range catalog.Claims {
		if _, exists := expectedOwners[claim.Owner]; !exists {
			return fmt.Errorf("composition catalog claim %s %q has undeclared owner %q", claim.Kind, claim.Key, claim.Owner)
		}
	}
	return nil
}

func validateCatalogInputs(inputs []CatalogInput) error {
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		switch input.Kind {
		case "frontend", "settings", "permissions", "fixtures":
		default:
			return fmt.Errorf("composition catalog input kind %q is invalid", input.Kind)
		}
		if err := validateRelativePath("composition catalog input", input.Path); err != nil {
			return err
		}
		if strings.TrimSpace(input.Identity) == "" || !digestPattern.MatchString(input.Digest) {
			return fmt.Errorf("composition catalog input %s %q is incomplete", input.Kind, input.Path)
		}
		key := input.Kind + "\x00" + input.Path
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate composition catalog input %s %q", input.Kind, input.Path)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func descriptorClaims(descriptor *Descriptor) []Claim {
	claims := make([]Claim, 0, len(descriptor.Bindings)+len(descriptor.Contributions.Frontend))
	for _, binding := range descriptor.Bindings {
		claims = append(claims, Claim{Kind: CollisionServiceBinding, Key: binding.Plugin + "/" + binding.Alias, Owner: "consumer"})
	}
	for _, contribution := range descriptor.Contributions.Frontend {
		claims = append(claims, Claim{Kind: CollisionPackage, Key: contribution.Export, Owner: contribution.Path})
	}
	return claims
}
