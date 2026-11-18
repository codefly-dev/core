package module

import (
	"reflect"
	"testing"
)

// agentMethods is what module agent's method policy answers for its calls:
// what each requires and, beside it, what it carries.
var agentMethods = declaredCallees{"agent": {
	"/agent.v1.TurnsService/Run":  {"agent.turns:run"},
	"/agent.v1.TurnsService/Read": {"agent.turns:read", "agent.transcripts:read_content"},
}}

// A binding names the methods it calls instead of the audience's kinds, and a
// role includes the audience's bundle instead of them too: the bundle's direct
// scopes are then held at that audience exactly as when the binding spelled
// its ceiling out, so a unit names none of another module's kinds anywhere.
func TestAnIncludedScopeIsHeldAtABindingThatNamesItsCalls(t *testing.T) {
	spelled := bundleFixture(t, "role-includes-bundles.yaml")
	published := WithBundles(PublishedContracts(agentBundles(t)))
	want, err := spelled.Resolve(solutionValues(), published)
	if err != nil {
		t.Fatal(err)
	}
	calling := bundleFixture(t, "role-includes-bundles.yaml")
	calling.Bindings[0].ScopeCeiling = nil
	calling.Bindings[0].Calls = map[string][]string{OperationInvoke: {"/agent.v1.TurnsService/Run", "/agent.v1.TurnsService/Read"}}
	if err := calling.Validate(); err != nil {
		t.Fatal(err)
	}
	got, err := calling.Resolve(solutionValues(), published, WithCallees(agentMethods))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Roles, want.Roles) {
		t.Fatalf("with calls %+v, spelled %+v", got.Roles, want.Roles)
	}
	// The ceiling read from the calls holds what they carry beside what they
	// require; a role is granted only what it names of it.
	if scopes := got.Bindings[0].Scopes[OperationInvoke]; !reflect.DeepEqual(scopes, []string{"agent.transcripts:read_content", "agent.turns:read", "agent.turns:run"}) {
		t.Fatalf("the binding's ceiling: %v", scopes)
	}
	if grants := got.Roles[1].Grants["agent"]; !reflect.DeepEqual(grants, []string{"agent.turns:run"}) {
		t.Fatalf("user is granted %v at agent", grants)
	}
}

// What a headless binding's calls require is never held for a role: no person
// is in one.
func TestAHeadlessBindingsCallsHoldNoRoleScope(t *testing.T) {
	calling := bundleFixture(t, "role-includes-bundles.yaml")
	calling.Bindings[0].ScopeCeiling = nil
	calling.Bindings[0].Operations = []string{OperationHeadless}
	calling.Bindings[0].Calls = map[string][]string{OperationHeadless: {"/agent.v1.TurnsService/Run"}}
	calling.Roles = []Role{{Name: "user", Scopes: []CeilingScope{{ResourceKind: "agent.turns", Actions: []string{"run"}}}}}
	got, err := calling.Resolve(solutionValues(), WithCallees(agentMethods))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Roles[0].Grants) != 0 {
		t.Fatalf("a headless binding underwrote a role scope: %v", got.Roles[0].Grants)
	}
}
