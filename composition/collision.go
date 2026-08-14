package composition

import (
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
		if os.IsNotExist(err) {
			return &Catalog{Schema: catalogSchema}, nil
		}
		return nil, err
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
	return &catalog, nil
}

func descriptorClaims(descriptor *Descriptor) []Claim {
	claims := make([]Claim, 0, len(descriptor.Bindings)+len(descriptor.Contributions.Settings)+len(descriptor.Contributions.Frontend))
	for _, binding := range descriptor.Bindings {
		claims = append(claims, Claim{Kind: CollisionServiceBinding, Key: binding.Plugin + "/" + binding.Alias, Owner: "consumer"})
	}
	for _, contribution := range descriptor.Contributions.Settings {
		claims = append(claims, Claim{Kind: CollisionSettingsField, Key: contribution.Message, Owner: contribution.Path})
	}
	for _, contribution := range descriptor.Contributions.Frontend {
		claims = append(claims, Claim{Kind: CollisionPackage, Key: contribution.Export, Owner: contribution.Path})
	}
	return claims
}
