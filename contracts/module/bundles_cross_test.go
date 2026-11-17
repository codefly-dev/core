package module

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// publisher is a contract publishing only bundles, for the tests below.
func publisher(t *testing.T, principal, bundles string) *Contract {
	t.Helper()
	return parsed(t, "schema: codefly/module-contract/v1\nprincipal: "+principal+
		"\nnamespaces: []\nqueues: []\nscope_ceilings: []\nbindings: []\ndestinations: []\nbundles:\n"+bundles)
}

// agentIncluding is module agent, whose screen bundle reaches two other
// modules' bundles through agent, beside one scope neither of them publishes
// in a bundle and agent therefore spells by hand.
func agentIncluding(t *testing.T) *Contract {
	return publisher(t, "agent", `
    - name: talk
      includes:
          - {module: model, bundle: infer, through: agent}
          - {module: notes, bundle: annotate, through: agent}
      scopes:
          - {resource_kind: agent.turns, actions: [run]}
          - {resource_kind: notes, actions: [redact], through: agent}
`)
}

// agentSpelled is the same bundle with the other modules' kinds hand-listed.
func agentSpelled(t *testing.T) *Contract {
	return publisher(t, "agent", `
    - name: talk
      scopes:
          - {resource_kind: agent.turns, actions: [run]}
          - {resource_kind: model.profiles, actions: [invoke, read], through: agent}
          - {resource_kind: notes, actions: [read, write, redact], through: agent}
`)
}

func modelBundles(t *testing.T) *Contract {
	return publisher(t, "model", `
    - name: discover
      scopes: [{resource_kind: model.profiles, actions: [read]}]
    - name: infer
      includes: [discover]
      scopes: [{resource_kind: model.profiles, actions: [invoke]}]
`)
}

// notesBundles keeps redact in no bundle: no screen is offered it.
func notesBundles(t *testing.T) *Contract {
	return publisher(t, "notes", `
    - name: read
      scopes: [{resource_kind: notes, actions: [read]}]
    - name: annotate
      includes: [read]
      scopes: [{resource_kind: notes, actions: [write]}]
`)
}

// A bundle includes another module's bundle, reached through the includer:
// the included bundle's scopes are granted exactly as if the includer spelled
// each with through: itself, beside the scopes it does spell.
func TestABundleIncludesAnotherModulesBundleThroughItself(t *testing.T) {
	published := PublishedContracts(agentIncluding(t), modelBundles(t), notesBundles(t))
	got, err := published.Bundle("agent", "talk")
	if err != nil {
		t.Fatal(err)
	}
	want := []CeilingScope{
		{ResourceKind: "agent.turns", Actions: []string{"run"}},
		{ResourceKind: "model.profiles", Actions: []string{"invoke", "read"}, Through: "agent"},
		{ResourceKind: "notes", Actions: []string{"read", "redact", "write"}, Through: "agent"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("talk expands to %+v, want %+v", got, want)
	}
	spelled, err := PublishedContracts(agentSpelled(t)).Bundle("agent", "talk")
	if err != nil || !reflect.DeepEqual(got, spelled) {
		t.Fatalf("included %+v, spelled %+v (%v)", got, spelled, err)
	}
}

// The unit including such a bundle is held exactly as for the spelled form:
// it reaches every kind through its one binding a person causes to the
// includer, and without one it is refused.
func TestAUnitIncludingSuchABundleStillNeedsItsBindingToTheIncluder(t *testing.T) {
	solution := bundleFixture(t, "role-includes-bundles.yaml")
	solution.Roles = solution.Roles[1:] // user includes agent/talk
	including, err := solution.Resolve(solutionValues(), WithBundles(PublishedContracts(agentIncluding(t), modelBundles(t), notesBundles(t))))
	if err != nil {
		t.Fatal(err)
	}
	spelled, err := solution.Resolve(solutionValues(), WithBundles(PublishedContracts(agentSpelled(t))))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(including.Roles, spelled.Roles) {
		t.Fatalf("included %+v, spelled %+v", including.Roles, spelled.Roles)
	}
	if got := including.Roles[0].Through["agent"]; !reflect.DeepEqual(got, []string{"model.profiles:invoke", "model.profiles:read", "notes:read", "notes:redact", "notes:write"}) {
		t.Fatalf("through the binding to agent: %v", got)
	}
	solution.Bindings[0].Audience = Slot{From: "assistant/other-audience"}
	values := MapValues{Public: map[string]map[string]string{"assistant": {"other-audience": "other"}}}
	_, err = solution.Resolve(values, WithBundles(PublishedContracts(agentIncluding(t), modelBundles(t), notesBundles(t))))
	if !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), "through agent: the unit declares no binding a person causes to agent") {
		t.Fatalf("no binding to the includer: %v", err)
	}
}

// What the included bundle itself reaches onward is reached through the
// includer and nowhere else: one hop is named, as the spelled form names one,
// and the including unit is asked for no binding to the module further on.
func TestAnIncludedBundlesOnwardScopesAreReachedThroughTheIncluderAlone(t *testing.T) {
	model := publisher(t, "model", `
    - name: infer
      scopes:
          - {resource_kind: model.profiles, actions: [invoke]}
          - {resource_kind: ledger, actions: [read], through: model}
`)
	got, err := PublishedContracts(agentIncluding(t), model, notesBundles(t)).Bundle("agent", "talk")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range got {
		if scope.Through != "" && scope.Through != "agent" {
			t.Fatalf("%s is reached through %s; agent's bundle reaches through agent alone", scope.ResourceKind, scope.Through)
		}
	}
	if !containsScope(got, CeilingScope{ResourceKind: "ledger", Actions: []string{"read"}, Through: "agent"}) {
		t.Fatalf("ledger is not reached through agent: %+v", got)
	}
}

func containsScope(scopes []CeilingScope, want CeilingScope) bool {
	for _, scope := range scopes {
		if reflect.DeepEqual(scope, want) {
			return true
		}
	}
	return false
}

// A bundle grants each permission once, held after its includes are expanded:
// one it spells that an include also grants is refused naming both.
func TestABundleSpellingWhatItIncludesIsRefused(t *testing.T) {
	agent := agentIncluding(t)
	agent.Bundles[0].Scopes[1].Actions = []string{"redact", "write"}
	_, err := PublishedContracts(agent, modelBundles(t), notesBundles(t)).Bundle("agent", "talk")
	if !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), `agent bundle "talk" includes notes/annotate, which grants notes:write through agent that the bundle also grants itself`) {
		t.Fatalf("a permission granted twice: %v", err)
	}
	// The same holds for a bundle of its own module.
	model := modelBundles(t)
	model.Bundles[1].Scopes[0].Actions = []string{"invoke", "read"}
	_, err = model.Bundle("infer")
	if !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), `model bundle "infer" includes discover, which grants model.profiles:read that the bundle also grants itself`) {
		t.Fatalf("a permission granted twice by an own include: %v", err)
	}
}

// An included bundle that cannot be read is refused naming the includer, the
// module, the bundle and what is missing.
func TestAnIncludedBundleThatCannotBeReadIsRefusedByName(t *testing.T) {
	// The publisher's contract was not supplied.
	_, err := PublishedContracts(agentIncluding(t), notesBundles(t)).Bundle("agent", "talk")
	if !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), `agent bundle "talk" includes model/infer: no contract was supplied for module model (supplied: agent, notes)`) {
		t.Fatalf("publisher not supplied: %v", err)
	}
	// The publisher publishes no such bundle.
	model := modelBundles(t)
	model.Bundles = model.Bundles[:1]
	_, err = PublishedContracts(agentIncluding(t), model, notesBundles(t)).Bundle("agent", "talk")
	if !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), `agent bundle "talk" includes model/infer: model publishes no bundle "infer" (it publishes discover)`) {
		t.Fatalf("bundle not published: %v", err)
	}
	// One contract alone cannot read another module's bundle, and says so.
	_, err = agentIncluding(t).Bundle("talk")
	if !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), `agent bundle "talk" includes model/infer: another module's bundle is read from the contract that module publishes`) {
		t.Fatalf("no publishers: %v", err)
	}
	// The role including it names the whole chain.
	solution := bundleFixture(t, "role-includes-bundles.yaml")
	_, err = solution.Resolve(solutionValues(), WithBundles(PublishedContracts(agentIncluding(t), notesBundles(t))))
	if !errors.Is(err, ErrUnresolvedInclude) || !strings.Contains(err.Error(), `role admin includes agent/talk: agent bundle "talk" includes model/infer: no contract was supplied for module model`) {
		t.Fatalf("through a role: %v", err)
	}
}

// Includes that come back to a bundle across modules are refused naming the
// chain.
func TestABundleIncludingItselfAcrossModulesIsRefused(t *testing.T) {
	model := publisher(t, "model", `
    - name: infer
      includes: [{module: agent, bundle: talk, through: model}]
      scopes: [{resource_kind: model.profiles, actions: [invoke]}]
`)
	_, err := PublishedContracts(agentIncluding(t), model, notesBundles(t)).Bundle("agent", "talk")
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "includes itself across modules (agent/talk -> model/infer -> agent/talk)") {
		t.Fatalf("cycle: %v", err)
	}
}

// An include is a bundle's name, or another module's bundle with the module
// it is reached through; any other key or shape is refused, and both forms
// are written back as they were read.
func TestABundleIncludeIsStrictlyDecodedAndWrittenBack(t *testing.T) {
	for name, include := range map[string]string{
		"an unknown key":   "{module: model, bundle: infer, through: agent, direct: true}",
		"no module":        "{bundle: infer, through: agent}",
		"a list":           "[infer]",
		"a nested mapping": "{module: {name: model}, bundle: infer, through: agent}",
	} {
		_, err := Parse([]byte("schema: codefly/module-contract/v1\nprincipal: agent\nnamespaces: []\nqueues: []\nscope_ceilings: []\nbindings: []\ndestinations: []\nbundles:\n    - name: talk\n      includes: [" + include + "]\n"))
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "a bundle include is") {
			t.Errorf("%s: %v", name, err)
		}
	}
	agent := agentIncluding(t)
	agent.Bundles = append(agent.Bundles, Bundle{Name: "review", Includes: []BundleInclude{{Bundle: "talk"}}})
	document, err := agent.Encode()
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(document)
	if err != nil || !reflect.DeepEqual(again.Bundles, agent.Bundles) {
		t.Fatalf("written back as %s: %+v (%v)", document, again.Bundles, err)
	}
	if !strings.Contains(string(document), "- talk\n") {
		t.Fatalf("an own include is written as its name:\n%s", document)
	}
}
