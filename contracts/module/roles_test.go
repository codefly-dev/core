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

// TestAScopeReachedThroughABindingIsCarriedPerBinding: a role scope naming
// through is addressed to no audience here — the authority addresses it in
// the composition — and Resolve carries it per binding in Through, beside
// what the role grants on the unit itself; a shared ceiling keeps its
// declaration through Resolve and Encode.
func TestAScopeReachedThroughABindingIsCarriedPerBinding(t *testing.T) {
	document, err := fixtures.ReadFile("testdata/roles-reach-through.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(document)
	if err != nil {
		t.Fatal(err)
	}
	values := MapValues{Public: map[string]map[string]string{"assistant": {"evidence-audience": "agent", "model-audience": "models"}}}
	resolved, err := contract.Resolve(values)
	if err != nil {
		t.Fatal(err)
	}
	want := []ResolvedRole{
		{Name: InstallerRole, Grants: map[string][]string{"assistant": {"assistant.tasks:read", "assistant.tasks:run"}}},
		{Name: "viewer", Grants: map[string][]string{"assistant": {"assistant.tasks:read"}, "models": {"model.profiles:read"}},
			Through: map[string][]string{
				"agent": {"documents:read", "model.profiles:invoke", "model.profiles:read"},
				"model": {"documents:read"},
			}},
	}
	if !reflect.DeepEqual(resolved.Roles, want) {
		t.Fatalf("Resolve addressed %#v, want %#v", resolved.Roles, want)
	}
	shared := func(ceilings []ScopeCeiling) []string {
		var out []string
		for _, ceiling := range ceilings {
			if ceiling.Shared {
				out = append(out, ceiling.ResourceKind)
			}
		}
		return out
	}
	if got := shared(resolved.ScopeCeilings); !reflect.DeepEqual(got, []string{"assistant.catalogue"}) {
		t.Fatalf("Resolve kept shared kinds %v", got)
	}
	encoded, err := contract.Encode()
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Roles, contract.Roles) || !reflect.DeepEqual(shared(again.ScopeCeilings), []string{"assistant.catalogue"}) {
		t.Fatalf("Encode lost a through or a shared declaration")
	}
}

// TestAScopeThroughAnOmittedBindingIsLeftOutWithIt: a grant through an
// optional binding the composition leaves out is left out with it, so the
// authority is never handed a reach-through keyed by a binding it has no
// resolution for.
func TestAScopeThroughAnOmittedBindingIsLeftOutWithIt(t *testing.T) {
	contract, err := Parse([]byte(`schema: codefly/module-contract/v1
principal: assistant
namespaces: []
queues: []
scope_ceilings:
    - resource_kind: assistant.tasks
      actions: [read]
roles:
    - name: installer
      scopes:
          - resource_kind: assistant.tasks
            actions: [read]
          - resource_kind: documents
            actions: [read]
            through: agent
bindings:
    - id: agent
      optional: true
      operations: [invoke]
      audience: {from: assistant/agent-audience}
      scope_ceiling:
          invoke:
              - resource_kind: agent.intents
                actions: [run]
destinations: []
`))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := contract.Resolve(MapValues{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Omitted) != 1 || resolved.Omitted[0].ID != "agent" {
		t.Fatalf("Resolve omitted %#v, want the agent binding", resolved.Omitted)
	}
	want := []ResolvedRole{{Name: InstallerRole, Grants: map[string][]string{"assistant": {"assistant.tasks:read"}}}}
	if !reflect.DeepEqual(resolved.Roles, want) {
		t.Fatalf("Resolve addressed %#v, want %#v", resolved.Roles, want)
	}
}

// TestSharedDecodingIsStrict: shared is a YAML boolean however it is written,
// including through an alias key at either level, which a check over raw key
// nodes would read as empty and pass.
func TestSharedDecodingIsStrict(t *testing.T) {
	const head = "schema: codefly/module-contract/v1\nprincipal: assistant\nnamespaces: []\nbindings: []\ndestinations: []\n"
	for name, body := range map[string]string{
		"word":             "queues: []\nscope_ceilings:\n    - {resource_kind: assistant.catalogue, actions: [read], shared: yes}\n",
		"quoted":           "queues: []\nscope_ceilings:\n    - {resource_kind: assistant.catalogue, actions: [read], shared: \"true\"}\n",
		"aliased value":    "queues: []\nrequire_method_policies: false\nscope_ceilings:\n    - resource_kind: &word yes\n      actions: [read]\n      shared: *word\n",
		"aliased key":      "queues: []\nscope_ceilings:\n    - resource_kind: assistant.catalogue\n      actions: [read, &k shared]\n      *k : yes\n",
		"aliased list key": "queues: [&q scope_ceilings]\n*q :\n    - {resource_kind: assistant.catalogue, actions: [read], shared: yes}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(head + body))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected invalid declaration, got %v", err)
			}
		})
	}
	contract, err := Parse([]byte(head + "queues: []\nscope_ceilings:\n    - {resource_kind: assistant.catalogue, actions: [read], shared: true}\n"))
	if err != nil || !contract.ScopeCeilings[0].Shared {
		t.Fatalf("shared: true read as %v, %v", contract, err)
	}
}

// TestThroughOnAConstructedBindingCeilingIsRefused: Parse refuses through on a
// binding ceiling's scope as an unknown field; a constructed contract is held
// to the same by Validate rather than resolved with it ignored.
func TestThroughOnAConstructedBindingCeilingIsRefused(t *testing.T) {
	document, err := fixtures.ReadFile("testdata/roles-reach-through.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(document)
	if err != nil {
		t.Fatal(err)
	}
	contract.Bindings[0].ScopeCeiling[OperationInvoke].Scopes[0].Through = "model"
	if err := contract.Validate(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), `binding agent invoke ceiling scope agent.intents names through "model"`) {
		t.Fatalf("Validate: %v", err)
	}
}
