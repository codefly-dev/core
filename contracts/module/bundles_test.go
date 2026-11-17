package module

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// publishedBundles answers bundles from the contracts of the modules that
// publish them, as a renderer reading the composed modules does.
type publishedBundles map[string]*Contract

func (p publishedBundles) Bundle(module, name string) ([]CeilingScope, error) {
	contract, ok := p[module]
	if !ok {
		return nil, fmt.Errorf("%s is not a module this composition holds", module)
	}
	return contract.Bundle(name)
}

// addressedOnly is roles as addressed, their scopes as granted left out: what
// the role tests below hold, each written before roles carried their scopes.
func addressedOnly(roles []ResolvedRole) []ResolvedRole {
	out := slices.Clone(roles)
	for i := range out {
		out[i].Scopes = nil
	}
	return out
}

func bundleFixture(t *testing.T, name string) *Contract {
	t.Helper()
	data, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

// A bundle expands every bundle it includes, into one deterministic answer:
// each kind once per place it is reached, actions sorted, kinds sorted,
// whatever order the publisher wrote them in.
func TestABundleExpandsItsIncludesDeterministically(t *testing.T) {
	agent := agentBundles(t)
	got, err := agent.Bundle("review")
	if err != nil {
		t.Fatal(err)
	}
	want := []CeilingScope{
		{ResourceKind: "agent.turns", Actions: []string{"read", "run"}},
		{ResourceKind: "notes", Actions: []string{"read", "write"}, Through: "agent"},
		{ResourceKind: "traces", Actions: []string{"read"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("review expands to %+v, want %+v", got, want)
	}
	// The same bundles declared in the other order expand the same.
	agent.Bundles[0], agent.Bundles[1] = agent.Bundles[1], agent.Bundles[0]
	agent.Bundles[1].Scopes[0], agent.Bundles[1].Scopes[1] = agent.Bundles[1].Scopes[1], agent.Bundles[1].Scopes[0]
	again, err := agent.Bundle("review")
	if err != nil || !reflect.DeepEqual(again, want) {
		t.Fatalf("reordered: %+v, %v", again, err)
	}
}

// A bundle the module does not publish is refused naming it; so is one whose
// includes come back to it, through any chain.
func TestAnUnknownBundleAndACycleAreRefusedByName(t *testing.T) {
	agent := agentBundles(t)
	if _, err := agent.Bundle("audit"); !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), `agent publishes no bundle "audit"`) {
		t.Fatalf("unknown bundle: %v", err)
	}
	agent.Bundles[0].Includes = []BundleInclude{{Bundle: "review"}}
	if _, err := agent.Bundle("review"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "includes itself (talk -> review -> talk)") {
		t.Fatalf("cycle: %v", err)
	}
	if err := agent.Validate(); err == nil || !strings.Contains(err.Error(), "includes itself") {
		t.Fatalf("a cycle validates: %v", err)
	}
}

// agentBundles is the published fixture as module agent publishes it: the
// kit's accepted documents are all module assistant's.
func agentBundles(t *testing.T) *Contract {
	t.Helper()
	agent := bundleFixture(t, "bundles-published.yaml")
	agent.Principal = "agent"
	for b := range agent.Bundles {
		for s := range agent.Bundles[b].Scopes {
			if agent.Bundles[b].Scopes[s].Through != "" {
				agent.Bundles[b].Scopes[s].Through = "agent"
			}
		}
	}
	return agent
}

func solutionValues() MapValues {
	return MapValues{Public: map[string]map[string]string{"assistant": {"agent-audience": "agent"}}}
}

// A role that includes bundles is resolved with their scopes as its own: a
// kind the bundle reaches directly at the audience underwriting it, one it
// reaches through its module downstream of the unit's binding to that module.
func TestARoleIncludingBundlesGrantsTheirScopes(t *testing.T) {
	solution := bundleFixture(t, "role-includes-bundles.yaml")
	resolved, err := solution.Resolve(solutionValues(), WithBundles(publishedBundles{"agent": agentBundles(t)}))
	if err != nil {
		t.Fatal(err)
	}
	admin, user := resolved.Roles[0], resolved.Roles[1]
	// The role's scopes as granted, includes and all: what a deliverer reads.
	if want := []CeilingScope{
		{ResourceKind: "agent.turns", Actions: []string{"read", "run"}},
		{ResourceKind: "notes", Actions: []string{"read", "write"}, Through: "agent"},
		{ResourceKind: "traces", Actions: []string{"read"}},
	}; !reflect.DeepEqual(admin.Scopes, want) {
		t.Fatalf("admin's scopes %+v, want %+v", admin.Scopes, want)
	}
	if got := admin.Grants["agent"]; !reflect.DeepEqual(got, []string{"agent.turns:read", "agent.turns:run"}) {
		t.Fatalf("admin grants at agent %v", got)
	}
	if got := admin.Through["agent"]; !reflect.DeepEqual(got, []string{"notes:read", "notes:write"}) {
		t.Fatalf("admin through agent %v", got)
	}
	if got := user.Grants["agent"]; !reflect.DeepEqual(got, []string{"agent.turns:run"}) {
		t.Fatalf("user grants at agent %v", got)
	}
	// traces is reached directly and no binding of the unit underwrites it:
	// addressed nowhere, as any literal scope no audience underwrites.
	if _, held := admin.Grants["traces"]; held {
		t.Fatalf("an unwritten kind was addressed: %v", admin.Grants)
	}
}

// An include is read from the module publishing it: with no bundle
// declarations to read it from, an unknown bundle, or no binding to reach the
// module through, the resolution refuses naming the role and the include.
func TestAnIncludeThatCannotBeGrantedIsRefusedByName(t *testing.T) {
	solution := bundleFixture(t, "role-includes-bundles.yaml")
	if _, err := solution.Resolve(solutionValues()); !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), "role admin includes agent/talk, and the contract was resolved with no bundle declarations") {
		t.Fatalf("no bundles: %v", err)
	}
	if _, err := solution.Resolve(solutionValues(), WithBundles(publishedBundles{})); !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), "agent is not a module this composition holds") {
		t.Fatalf("unknown module: %v", err)
	}
	solution.Bindings[0].Audience = Slot{From: "assistant/other-audience"}
	values := MapValues{Public: map[string]map[string]string{"assistant": {"other-audience": "other"}}}
	_, err := solution.Resolve(values, WithBundles(publishedBundles{"agent": agentBundles(t)}))
	if !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), "reaches notes through agent: the unit declares no binding a person causes to agent") {
		t.Fatalf("no binding to the module: %v", err)
	}
}

// A role grants each permission once: one it also grants itself beside an
// include is refused naming both, so its own scopes are its page's own calls.
func TestARoleGrantingWhatItIncludesIsRefused(t *testing.T) {
	solution := bundleFixture(t, "role-includes-bundles.yaml")
	solution.Roles[1].Scopes = []CeilingScope{{ResourceKind: "agent.turns", Actions: []string{"run"}}}
	_, err := solution.Resolve(solutionValues(), WithBundles(publishedBundles{"agent": agentBundles(t)}))
	if !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), "role user includes agent/talk, which grants agent.turns:run that the role also grants itself") {
		t.Fatalf("a permission granted twice: %v", err)
	}
}

// Callees that also answer bundles serve both through WithCallees, so a
// renderer passing its one reader of the composed modules resolves includes.
func TestCalleesThatAnswerBundlesResolveIncludes(t *testing.T) {
	solution := bundleFixture(t, "role-includes-bundles.yaml")
	both := struct {
		declaredCallees
		publishedBundles
	}{declaredCallees{}, publishedBundles{"agent": agentBundles(t)}}
	if _, err := solution.Resolve(solutionValues(), WithCallees(both)); err != nil {
		t.Fatal(err)
	}
}
