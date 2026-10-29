package module

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestInstallationDeclarationsSurviveEncodeAndResolve(t *testing.T) {
	data, err := os.ReadFile("testdata/roles.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{"", "none", "editor"} {
		t.Run(selector, func(t *testing.T) {
			contract, err := Parse(append(append([]byte(nil), data...), []byte("installation_role: \""+selector+"\"\nedge_dependencies: [{module: catalogue.other, presence: required}]\n")...))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := contract.Encode()
			if err != nil {
				t.Fatal(err)
			}
			again, err := Parse(encoded)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := again.Resolve(values())
			if err != nil {
				t.Fatal(err)
			}
			if again.InstallationRole != selector || resolved.InstallationRole != selector || !reflect.DeepEqual(resolved.EdgeDependencies, contract.EdgeDependencies) {
				t.Fatalf("declarations changed: parsed %+v, resolved %+v", again, resolved)
			}
			if resolved.Bindings[0].ID != "documents" || resolved.EdgeDependencies[0].Module != "catalogue.other" || len(resolved.EdgeDependencies) != 1 {
				t.Fatal("binding identity changed or became a dependency")
			}
			resolved.EdgeDependencies[0].Module = "changed"
			if again.EdgeDependencies[0].Module != "catalogue.other" {
				t.Fatal("Resolve aliased declaration storage")
			}
		})
	}
	contract, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := contract.Resolve(values())
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.EdgeDependencies) != 0 {
		t.Fatal("bindings inferred undeclared dependencies")
	}
	contract.InstallationRole = "editor"
	contract.Roles[1].Scopes = nil
	if err := contract.Validate(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "grants nothing") {
		t.Fatalf("selected empty role escaped the existing rule: %v", err)
	}
	contract.InstallationRole = "none"
	if err := contract.Validate(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "grants nothing") {
		t.Fatalf("none waived an existing role rule: %v", err)
	}
}

func TestInstallationDeclarationShapesAreStrict(t *testing.T) {
	for _, field := range []string{
		"installation_role: true", "installation_role: 42", "installation_role: null",
		"installation_role: []", "installation_role: {}", "installation_role: !custom none",
		"edge_dependencies: {}", "edge_dependencies: [42]", "edge_dependencies: null",
		"edge_dependencies: [{module: false, presence: required}]",
		"edge_dependencies: [{module: !custom other, presence: required}]",
		"edge_dependencies: [{module: other, presence: true}]",
		"edge_dependencies: [{module: other, presence: null}]",
		"edge_dependencies: [{module: other}]",
		"edge_dependencies: [{presence: required}]",
		"edge_dependencies: [{module: other, presence: required, operations: [read]}]",
		"edge_dependencies: [{module: other, module: another, presence: required}]",
		"edge_dependencies: [{module: &identity 42, presence: required}, {module: *identity, presence: optional}]",
		"installation_role: none\ninstallation_role: ghost",
	} {
		t.Run(field, func(t *testing.T) {
			if _, err := Parse(append(fixture(t), []byte(field+"\n")...)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid declaration accepted: %v", err)
			}
		})
	}
}

func TestProgrammaticInstallationDeclarationsUseTheSameRules(t *testing.T) {
	for _, change := range []func(*Contract){
		func(c *Contract) { c.InstallationRole = "ghost" },
		func(c *Contract) { c.InstallationRole = " none " },
		func(c *Contract) { c.EdgeDependencies = []EdgeDependency{{Module: "", Presence: "required"}} },
		func(c *Contract) { c.EdgeDependencies = []EdgeDependency{{Module: " other ", Presence: "required"}} },
		func(c *Contract) {
			c.EdgeDependencies = []EdgeDependency{{Module: "other", Presence: "required"}, {Module: "other", Presence: "optional"}}
		},
		func(c *Contract) { c.EdgeDependencies = []EdgeDependency{{Module: "other", Presence: ""}} },
	} {
		contract, err := Parse(fixture(t))
		if err != nil {
			t.Fatal(err)
		}
		change(contract)
		if !errors.Is(contract.Validate(), ErrInvalid) {
			t.Fatal("Validate accepted invalid declaration")
		}
		if _, err := contract.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Encode: %v", err)
		}
		if _, err := contract.Resolve(values()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Resolve: %v", err)
		}
	}
}

func TestInstallationFieldsDoNotChangeLegacyEncodedBytes(t *testing.T) {
	// Captured from every accepted fixture at ef374a91 before adding fields.
	data, err := os.ReadFile("testdata/legacy-installation-encode.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]string
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	all, err := Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range all {
		want, legacy := expected[fixture.Name]
		if !legacy {
			continue
		}
		contract, err := Parse(fixture.Document)
		if err != nil {
			t.Fatal(err)
		}
		for _, empty := range [][]EdgeDependency{nil, {}} {
			contract.EdgeDependencies = empty
			encoded, err := contract.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(encoded)); got != want {
				t.Fatalf("legacy fixture %s bytes changed: %s != %s", fixture.Name, got, want)
			}
		}
		delete(expected, fixture.Name)
	}
	if len(expected) != 0 {
		t.Fatalf("legacy fixtures missing: %v", expected)
	}
}
