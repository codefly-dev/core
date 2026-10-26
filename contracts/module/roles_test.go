package module

import (
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestRolesAreReadAsDeclaredAndWrittenBack: a unit's roles are part of its
// contract, read in order with every scope as written, and Encode writes them
// back so the bytes read again to the same roles.
func TestRolesAreReadAsDeclaredAndWrittenBack(t *testing.T) {
	document, err := os.ReadFile("testdata/roles.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(document)
	if err != nil {
		t.Fatal(err)
	}
	want := []Role{
		{Name: InstallerRole, Scopes: []CeilingScope{
			{ResourceKind: "assistant.tasks", Actions: []string{"read", "write", "publish"}},
			{ResourceKind: "documents", Actions: []string{"read", "write"}},
		}},
		{Name: "editor", Scopes: []CeilingScope{{ResourceKind: "assistant.tasks", Actions: []string{"read", "write"}}}},
		{Name: "viewer", Scopes: []CeilingScope{
			{ResourceKind: "assistant.tasks", Actions: []string{"read"}},
			{ResourceKind: "documents", Actions: []string{"read"}},
		}},
	}
	if !reflect.DeepEqual(contract.Roles, want) {
		t.Fatalf("roles read as %+v, declared as %+v", contract.Roles, want)
	}
	encoded, err := contract.Encode()
	if err != nil {
		t.Fatalf("a contract with roles must encode: %v", err)
	}
	again, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Roles, want) {
		t.Fatalf("roles written and read back as %+v", again.Roles)
	}
}

// TestAContractWithoutRolesDeclaresNone: a bare module's access is the
// organisation's roles, so it writes no roles key and Encode writes none.
func TestAContractWithoutRolesDeclaresNone(t *testing.T) {
	contract, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if contract.Roles != nil {
		t.Fatalf("a contract declaring no roles reads as %+v", contract.Roles)
	}
	encoded, err := contract.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := Parse(encoded); again.Roles != nil {
		t.Fatalf("Encode wrote roles for a contract declaring none")
	}
}

func rolesGrantBindingsContract(t *testing.T) *Contract {
	t.Helper()
	document, err := fixtures.ReadFile("testdata/roles-grant-bindings.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(document)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func rolesValues(replica string, owners string) MapValues {
	return MapValues{Public: map[string]map[string]string{"assistant": {
		"evidence-audience": "documents", "documents-audience": "documents", "replica-audience": replica, "owner-audiences": owners,
	}}}
}

var rolesDeclarations = declaredCallees{
	"documents": {
		"/documents.v1.DocumentsService/ResolveEvidence":     {"documents:read", "documents.collection:read"},
		"/documents.v1.DocumentsService/ReadDocumentExcerpt": {"documents:read"},
		"/targets.v1.TargetService/Reach":                    {"documents.versions:read"},
	},
	"robin": {"/targets.v1.TargetService/Reach": {"robin.answers:read"}},
}

// TestARoleGrantsWhatABindingsCallsRequire: a role naming a binding that uses
// calls grants what those calls require, derived exactly as the binding's
// ceiling is — at each audience it is delivered to, one per listed audience —
// and every grant, its own kinds and the scopes it spells out included,
// carries the audience it is held at.
func TestARoleGrantsWhatABindingsCallsRequire(t *testing.T) {
	resolved, err := rolesGrantBindingsContract(t).Resolve(rolesValues("documents", "robin,documents"), WithCallees(rolesDeclarations))
	if err != nil {
		t.Fatal(err)
	}
	want := []ResolvedRole{
		{Name: InstallerRole, Grants: map[string][]string{
			"assistant": {"assistant.tasks:read", "assistant.tasks:write"},
			"documents": {"documents.collection:read", "documents.versions:read", "documents:read"},
			"robin":     {"robin.answers:read"},
		}},
		{Name: "viewer", Grants: map[string][]string{"documents": {"documents:read"}}},
	}
	if !reflect.DeepEqual(resolved.Roles, want) {
		t.Fatalf("roles resolve to %+v, want %+v", resolved.Roles, want)
	}

	// The binding's calls are read with it: a role granting one cannot be
	// resolved without the callee declarations it is derived from.
	if _, err := rolesGrantBindingsContract(t).Resolve(rolesValues("documents", "robin,documents")); !errors.Is(err, ErrUnresolvedCall) {
		t.Fatalf("a role granting a binding's calls with no callees to read them from: %v", err)
	}
}

// TestARoleGrantThroughAnOmittedBindingIsOmitted: an optional binding the
// composition leaves out delivers nothing, so a role grants nothing through it.
func TestARoleGrantThroughAnOmittedBindingIsOmitted(t *testing.T) {
	resolved, err := rolesGrantBindingsContract(t).Resolve(rolesValues("documents", ""), WithCallees(rolesDeclarations))
	if err != nil {
		t.Fatal(err)
	}
	installer := resolved.Roles[0].Grants
	if _, granted := installer["robin"]; granted || slices.Contains(installer["documents"], "documents.versions:read") {
		t.Fatalf("the omitted owner binding still grants: %+v", installer)
	}
}

// TestARoleScopeTwoAudiencesUnderwriteIsRefused: a role scope names no
// audience, which is unambiguous while one audience underwrites it. Two
// bindings spelling it out toward two different audiences leave the role
// saying nothing about which it means, so the resolution refuses it, naming
// both; toward one audience it is one grant.
func TestARoleScopeTwoAudiencesUnderwriteIsRefused(t *testing.T) {
	_, err := rolesGrantBindingsContract(t).Resolve(rolesValues("replica", "robin,documents"), WithCallees(rolesDeclarations))
	if !errors.Is(err, ErrAmbiguousSlot) || !strings.Contains(err.Error(), "role installer grants documents:read, which is underwritten at documents and replica") {
		t.Fatalf("a role scope two audiences underwrite: %v", err)
	}
}

func installationOwnedContract(t *testing.T) *Contract {
	t.Helper()
	document, err := fixtures.ReadFile("testdata/roles-installation-owned.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(document)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

// TestInstallationOwnedScopesAreReadAndWrittenBack: a role's
// installation_owned_scopes are read as declared, apart from its scopes, and
// Encode writes them back under the same key.
func TestInstallationOwnedScopesAreReadAndWrittenBack(t *testing.T) {
	contract := installationOwnedContract(t)
	want := []Role{
		{Name: InstallerRole,
			Scopes: []CeilingScope{
				{ResourceKind: "assistant.tasks", Actions: []string{"read", "write"}},
				{ResourceKind: "documents.collection", Actions: []string{"read"}},
			},
			InstallationOwnedScopes: []CeilingScope{{ResourceKind: "documents.collection", Actions: []string{"read", "write"}}}},
		{Name: "viewer",
			Scopes:                  []CeilingScope{{ResourceKind: "assistant.tasks", Actions: []string{"read"}}},
			InstallationOwnedScopes: []CeilingScope{{ResourceKind: "documents.collection", Actions: []string{"read"}}}},
		{Name: "auditor", InstallationOwnedScopes: []CeilingScope{
			{ResourceKind: "assistant.tasks", Actions: []string{"read"}},
			{ResourceKind: "documents.collection", Actions: []string{"read"}},
		}},
	}
	if !reflect.DeepEqual(contract.Roles, want) {
		t.Fatalf("roles read as %+v, declared as %+v", contract.Roles, want)
	}
	encoded, err := contract.Encode()
	if err != nil {
		t.Fatalf("a contract with installation-owned scopes must encode: %v", err)
	}
	if !strings.Contains(string(encoded), "installation_owned_scopes:") {
		t.Fatalf("Encode wrote the installation-owned scopes under another key:\n%s", encoded)
	}
	again, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Roles, want) {
		t.Fatalf("roles written and read back as %+v", again.Roles)
	}
}

// TestInstallationOwnedScopesAreAddressedApart: each installation-owned scope
// is addressed to the one audience that underwrites it, exactly as a literal
// scope is, and kept apart from what the role grants on the whole kind; a role
// declaring none resolves none.
func TestInstallationOwnedScopesAreAddressedApart(t *testing.T) {
	values := MapValues{Public: map[string]map[string]string{"assistant": {"collections-audience": "documents"}}}
	resolved, err := installationOwnedContract(t).Resolve(values)
	if err != nil {
		t.Fatal(err)
	}
	want := []ResolvedRole{
		{Name: InstallerRole,
			Grants:                  map[string][]string{"assistant": {"assistant.tasks:read", "assistant.tasks:write"}, "documents": {"documents.collection:read"}},
			InstallationOwnedGrants: map[string][]string{"documents": {"documents.collection:read", "documents.collection:write"}}},
		{Name: "viewer",
			Grants:                  map[string][]string{"assistant": {"assistant.tasks:read"}},
			InstallationOwnedGrants: map[string][]string{"documents": {"documents.collection:read"}}},
		{Name: "auditor",
			Grants:                  map[string][]string{},
			InstallationOwnedGrants: map[string][]string{"assistant": {"assistant.tasks:read"}, "documents": {"documents.collection:read"}}},
	}
	if !reflect.DeepEqual(resolved.Roles, want) {
		t.Fatalf("roles resolve to %+v, want %+v", resolved.Roles, want)
	}
	plain, err := rolesGrantBindingsContract(t).Resolve(rolesValues("documents", "robin,documents"), WithCallees(rolesDeclarations))
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range plain.Roles {
		if role.InstallationOwnedGrants != nil {
			t.Fatalf("role %s declares no installation-owned scope and resolves %+v", role.Name, role.InstallationOwnedGrants)
		}
	}
}

// TestAnInstallationOwnedScopeTwoAudiencesUnderwriteIsRefused: an
// installation-owned scope names no audience either, so two audiences
// underwriting it are refused by name, as for a literal scope.
func TestAnInstallationOwnedScopeTwoAudiencesUnderwriteIsRefused(t *testing.T) {
	contract := rolesGrantBindingsContract(t)
	contract.Roles[1].InstallationOwnedScopes = []CeilingScope{{ResourceKind: "documents", Actions: []string{"read"}}}
	contract.Roles[0].Scopes = contract.Roles[0].Scopes[:1]
	if err := contract.Validate(); err != nil {
		t.Fatal(err)
	}
	_, err := contract.Resolve(rolesValues("replica", "robin,documents"), WithCallees(rolesDeclarations))
	if !errors.Is(err, ErrAmbiguousSlot) || !strings.Contains(err.Error(), "role viewer grants installation-owned documents:read, which is underwritten at documents and replica") {
		t.Fatalf("an installation-owned scope two audiences underwrite: %v", err)
	}
}
