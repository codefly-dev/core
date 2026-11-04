package module

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// perClassContract is one contract declaring one edge in each spelling: robin
// required wherever it is delivered, annotations optional on a development
// cell and required in production.
func perClassContract(t *testing.T) *Contract {
	t.Helper()
	data, err := os.ReadFile("testdata/roles.yaml")
	if err != nil {
		t.Fatal(err)
	}
	document := append(append([]byte(nil), data...), []byte(
		"edge_dependencies:\n"+
			"  - {module: robin, presence: required}\n"+
			"  - module: annotations\n"+
			"    presence_by_class: {development: optional, production: required}\n")...)
	contract, err := Parse(document)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

// TestAPerClassPresenceResolvesPerEnvironmentClass is the point of the
// declaration: ONE contract, delivered to two environments, carries optional
// to the development one and required to the production one — and in both the
// delivered entry carries a single presence and nothing of the per-class
// declaration, so the host's reading (`presence == "required"`) is unchanged
// and no host learns what an environment class is.
func TestAPerClassPresenceResolvesPerEnvironmentClass(t *testing.T) {
	contract := perClassContract(t)
	for class, want := range map[string]string{ClassDevelopment: PresenceOptional, ClassProduction: PresenceRequired} {
		t.Run(class, func(t *testing.T) {
			resolved, err := contract.Resolve(values(), WithEnvironmentClass(class))
			if err != nil {
				t.Fatal(err)
			}
			if len(resolved.EdgeDependencies) != 2 {
				t.Fatalf("resolved %d edges, want 2: %+v", len(resolved.EdgeDependencies), resolved.EdgeDependencies)
			}
			robin, annotations := resolved.EdgeDependencies[0], resolved.EdgeDependencies[1]
			if robin.Module != "robin" || robin.Presence != PresenceRequired {
				t.Errorf("the plain spelling moved with the class: %+v", robin)
			}
			if annotations.Module != "annotations" || annotations.Presence != want {
				t.Errorf("annotations resolved to %q in class %s, want %q", annotations.Presence, class, want)
			}
			if annotations.PresenceByClass != nil {
				t.Errorf("a resolved edge carries the per-class declaration: %+v", annotations)
			}
		})
	}
	// The contract itself is untouched by a resolution, so the same contract
	// resolves again for another class.
	if contract.EdgeDependencies[1].PresenceByClass[ClassProduction] != PresenceRequired {
		t.Error("resolution mutated the contract's own declaration")
	}
}

// TestAnUnnamedEnvironmentClassIsRefusedByName holds the three resolution
// refusals a per-class presence has. None of them is reachable by a document
// alone — a class is the deliverer's, not the contract's — and none of them
// is a default: a presence nobody declared for the environment being
// delivered to is refused naming the module, the classes the contract does
// name, and the class asked for.
func TestAnUnnamedEnvironmentClassIsRefusedByName(t *testing.T) {
	contract := perClassContract(t)
	for _, tc := range []struct {
		name, class string
		words       []string
	}{
		{
			name:  "no class declared",
			class: "",
			words: []string{"annotations", "development, production", "declares no class", "never read off an environment's name"},
		},
		{
			name:  "a class the contract does not name",
			class: ClassStaging,
			words: []string{"annotations", "development, production", `"staging"`, "refused rather than resolved to a default"},
		},
		{
			name:  "a class core does not know",
			class: "prod",
			words: []string{`"prod"`, "development, staging, production"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := contract.Resolve(values(), WithEnvironmentClass(tc.class))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("resolution accepted class %q: %v", tc.class, err)
			}
			for _, word := range tc.words {
				if !strings.Contains(err.Error(), word) {
					t.Errorf("the refusal does not say %s: %v", word, err)
				}
			}
		})
	}
}

// TestAPlainPresenceNeedsNoEnvironmentClass is why no existing contract and no
// existing environment changes: every contract in the fleet declares the plain
// spelling, and it resolves with no class declared, exactly as before.
func TestAPlainPresenceNeedsNoEnvironmentClass(t *testing.T) {
	data, err := os.ReadFile("testdata/edge-presence.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, class := range append([]string{""}, EnvironmentClasses()...) {
		resolved, err := contract.ResolveEdgeDependencies(class)
		if err != nil {
			t.Fatalf("class %q: %v", class, err)
		}
		if len(resolved) != 2 || resolved[0].Presence != PresenceRequired || resolved[1].Presence != PresenceOptional {
			t.Fatalf("class %q changed a plain presence: %+v", class, resolved)
		}
	}
}
