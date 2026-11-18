package module

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// selfIncluding is a module with a page: it publishes bundles that reach a
// consumed kind onward through itself, for another unit's screen, and its own
// page roles include those same bundles.
const selfIncluding = `schema: codefly/module-contract/v1
principal: assistant
namespaces: []
queues: []
scope_ceilings:
    - resource_kind: assistant.entries
      actions: [read, write]
roles:
    - name: admin
      includes: [{module: assistant, bundle: maintain}]
    - name: user
      includes: [{module: assistant, bundle: lookup}]
bindings:
    - id: rows
      operations: [invoke]
      audience: {from: assistant/records-audience}
      scope_ceiling:
          invoke:
              - resource_kind: records
                actions: [read, write]
bundles:
    - name: lookup
      scopes:
          - {resource_kind: assistant.entries, actions: [read]}
          - {resource_kind: records, actions: [read], through: assistant}
    - name: maintain
      includes: [lookup]
      scopes:
          - {resource_kind: assistant.entries, actions: [write]}
          - {resource_kind: records, actions: [write], through: assistant}
destinations: []
`

func parsed(t *testing.T, document string) *Contract {
	t.Helper()
	contract, err := Parse([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func selfValuesFor(audience string) MapValues {
	return MapValues{Public: map[string]map[string]string{"assistant": {"records-audience": audience}}}
}

// A unit's own page role includes a bundle the unit publishes. What that
// bundle reaches onward through the unit, for another unit's screen, the
// unit's own page reaches directly: it IS the unit, so the scope is the
// role's own direct scope on that kind, addressed as any literal scope is.
func TestAUnitIncludesItsOwnBundleReachingOnwardThroughItself(t *testing.T) {
	contract := parsed(t, selfIncluding)
	resolved, err := contract.Resolve(selfValuesFor("records"), WithBundles(PublishedContracts(contract)))
	if err != nil {
		t.Fatal(err)
	}
	admin, user := resolved.Roles[0], resolved.Roles[1]
	if want := []CeilingScope{
		{ResourceKind: "assistant.entries", Actions: []string{"read", "write"}},
		{ResourceKind: "records", Actions: []string{"read", "write"}},
	}; !reflect.DeepEqual(admin.Scopes, want) {
		t.Fatalf("admin's scopes %+v, want %+v", admin.Scopes, want)
	}
	if want := map[string][]string{
		"assistant": {"assistant.entries:read", "assistant.entries:write"},
		"records":   {"records:read", "records:write"},
	}; !reflect.DeepEqual(admin.Grants, want) {
		t.Fatalf("admin grants %v, want %v", admin.Grants, want)
	}
	if admin.Through != nil || user.Through != nil {
		t.Fatalf("a self-include reaches through nothing: %v, %v", admin.Through, user.Through)
	}
	if want := map[string][]string{"assistant": {"assistant.entries:read"}, "records": {"records:read"}}; !reflect.DeepEqual(user.Grants, want) {
		t.Fatalf("user grants %v, want %v", user.Grants, want)
	}

	// The spelled form it replaces resolves to the same roles.
	spelled := parsed(t, selfIncluding)
	spelled.Roles[0].Includes, spelled.Roles[1].Includes = nil, nil
	spelled.Roles[0].Scopes = []CeilingScope{
		{ResourceKind: "assistant.entries", Actions: []string{"read", "write"}},
		{ResourceKind: "records", Actions: []string{"read", "write"}},
	}
	spelled.Roles[1].Scopes = []CeilingScope{
		{ResourceKind: "assistant.entries", Actions: []string{"read"}},
		{ResourceKind: "records", Actions: []string{"read"}},
	}
	byHand, err := spelled.Resolve(selfValuesFor("records"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved.Roles, byHand.Roles) {
		t.Fatalf("included %+v, spelled %+v", resolved.Roles, byHand.Roles)
	}
}

// The unit holds the contract it is resolved from, so its own bundle is read
// from it: a self-include needs no other module's declarations.
func TestASelfIncludeIsReadFromTheUnitItself(t *testing.T) {
	if _, err := parsed(t, selfIncluding).Resolve(selfValuesFor("records")); err != nil {
		t.Fatal(err)
	}
}

// A role grants each permission once, held after the include is expanded: a
// self-included scope reached through the unit is the role's direct scope, so
// the role does not also list it by hand.
func TestASelfIncludedScopeTheRoleAlsoListsIsRefused(t *testing.T) {
	contract := parsed(t, selfIncluding)
	contract.Roles[0].Scopes = []CeilingScope{{ResourceKind: "records", Actions: []string{"write"}}}
	_, err := contract.Resolve(selfValuesFor("records"))
	if !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), "role admin includes assistant/maintain, which grants records:write that the role also grants itself") {
		t.Fatalf("a permission granted twice: %v", err)
	}
}

// Only a self-include reaches the unit's own onward scope directly. Another
// module's bundle that reaches a kind through this unit is still held to a
// binding a person causes to it, and refused naming what is missing.
func TestAnotherModulesBundleReachingThroughTheUnitStillNeedsABinding(t *testing.T) {
	contract := parsed(t, selfIncluding)
	contract.Roles[0].Includes = []RoleInclude{{Module: "agent", Bundle: "talk"}}
	agent := agentBundles(t)
	for s := range agent.Bundles[0].Scopes {
		if agent.Bundles[0].Scopes[s].Through != "" {
			agent.Bundles[0].Scopes[s].Through = "assistant"
		}
	}
	_, err := contract.Resolve(selfValuesFor("records"), WithBundles(PublishedContracts(contract, agent)))
	if !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), "reaches notes through assistant: the unit declares no binding a person causes to assistant") {
		t.Fatalf("no binding to itself: %v", err)
	}
}
