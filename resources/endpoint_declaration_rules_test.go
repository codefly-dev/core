package resources

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// readWithout decodes a service manifest the way the loader does and judges
// its endpoint declarations with one rule deleted — the reader the self-check
// proves each rule against. It is the loader's own endpoint pass
// (Service.postLoadEndpoints), not a copy of it.
func readWithout(deleted string) func(document []byte) error {
	return func(document []byte) error {
		service, err := LoadFromBytes[Service](document)
		if err != nil {
			return err
		}
		service.module = "alpha"
		return service.postLoadEndpoints(deleted)
	}
}

// TestEveryEndpointDeclarationRuleIsProtectedByAFixture is the kit's
// self-check: every rule ValidateEndpointDeclaration enforces is named by at
// least one refused fixture, every refused fixture names a rule that exists,
// and deleting any one rule fails the kit — on a fixture naming that rule,
// which the deletion lets through or hands to a later rule with another
// message. A rule this test cannot falsify is a rule the kit does not protect.
func TestEveryEndpointDeclarationRuleIsProtectedByAFixture(t *testing.T) {
	all, err := EndpointDeclarationFixtures()
	if err != nil {
		t.Fatal(err)
	}
	names := endpointDeclarationRuleNames()
	protected := map[string][]EndpointDeclarationFixture{}
	for _, fixture := range all {
		switch fixture.Outcome {
		case EndpointDeclarationAccepted:
			if fixture.Rule != "" {
				t.Errorf("accepted fixture %s names rule %s", fixture.Name, fixture.Rule)
			}
		case EndpointDeclarationRefused:
			if !slices.Contains(names, fixture.Rule) {
				t.Errorf("refused fixture %s names rule %q, which does not exist", fixture.Name, fixture.Rule)
			}
			protected[fixture.Rule] = append(protected[fixture.Rule], fixture)
		}
	}
	for _, rule := range endpointDeclarationRules() {
		fixtures := protected[rule.name]
		if len(fixtures) == 0 {
			t.Errorf("rule %s is protected by no fixture: a reader could drop it and pass the kit", rule.name)
			continue
		}
		// The kit, run against this reader with the rule deleted, must fail.
		recorder := &recordingKitT{}
		RunEndpointDeclarationKit(recorder, readWithout(rule.name))
		if recorder.failures == 0 {
			t.Errorf("rule %s can be deleted and the kit still passes: %d fixture(s) name it but none notices", rule.name, len(fixtures))
		}
		// And each fixture naming the rule is no longer refused BY it.
		for _, fixture := range fixtures {
			err := readWithout(rule.name)(fixture.Document)
			if err != nil && strings.Contains(err.Error(), fixture.Message) {
				t.Errorf("fixture %s is still refused naming %q with rule %s deleted: the refusal comes from somewhere else", fixture.Name, fixture.Message, rule.name)
			}
		}
	}
}

// TestEveryEndpointDeclarationFixtureReachesItsOutcome runs the kit against
// the real loader — a manifest on disk, read by LoadServiceFromDir, the way
// every consumer reads one — and holds the kit to its exact contents: a
// fixture that drifted from the loader, or one that was dropped, is caught
// here first.
func TestEveryEndpointDeclarationFixtureReachesItsOutcome(t *testing.T) {
	ctx := context.Background()
	RunEndpointDeclarationKit(t, func(document []byte) error {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ServiceConfigurationName), document, 0o644); err != nil {
			return err
		}
		_, err := LoadServiceFromDir(ctx, dir)
		return err
	})
	all, err := EndpointDeclarationFixtures()
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(all))
	for _, fixture := range all {
		names = append(names, fixture.Name)
	}
	sort.Strings(names)
	want := []string{
		"allow-modules-empty", "allow-modules-named", "allow-modules-on-public",
		"allow-modules-wildcard", "exposed-public", "exposure-on-external",
		"exposure-on-internal", "exposure-on-private", "exposure-unknown",
		"external-private", "location-unknown", "valid", "visibility-external",
		"visibility-module", "visibility-unknown",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("the kit ships %v; a changed kit is a changed rule set, declared here", names)
	}
}

// TestTheEndpointDeclarationKitFailsAReaderThatSkipsARule: a reader that
// decodes but does not judge the declaration passes the accepted fixtures and
// fails the refusals by name, which is what makes the kit a gate rather than
// a round-trip.
func TestTheEndpointDeclarationKitFailsAReaderThatSkipsARule(t *testing.T) {
	recorder := &recordingKitT{}
	RunEndpointDeclarationKit(recorder, func(document []byte) error {
		_, err := LoadFromBytes[Service](document)
		return err
	})
	if recorder.failures == 0 || !strings.Contains(recorder.messages, "fixture allow-modules-wildcard must be refused") {
		t.Fatalf("the kit did not name the rule the reader skipped:\n%s", recorder.messages)
	}
}

// TestEveryEndpointDeclarationRuleHoldsExactlyOneCondition enumerates the
// refusals in each rule's check from the package's own source: a second
// fmt.Errorf added inside an existing rule would be a condition no fixture is
// required to protect, and it fails at that function.
func TestEveryEndpointDeclarationRuleHoldsExactlyOneCondition(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "endpoint_declaration.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]int{}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || !strings.HasPrefix(function.Name.Name, "check") {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "fmt" && selector.Sel.Name == "Errorf" {
					checks[function.Name.Name]++
				}
			}
			return true
		})
	}
	if len(checks) != len(endpointDeclarationRules()) {
		t.Fatalf("%d check functions for %d rules", len(checks), len(endpointDeclarationRules()))
	}
	for name, conditions := range checks {
		if conditions != 1 {
			t.Errorf("%s holds %d refusals; a rule holds exactly one condition, so every condition has a fixture", name, conditions)
		}
	}
}

type recordingKitT struct {
	failures int
	messages string
}

func (r *recordingKitT) Helper() {}

func (r *recordingKitT) Errorf(format string, args ...any) {
	r.failures++
	r.messages += strings.TrimSpace(fmt.Sprintf(format, args...)) + "\n"
}

func (r *recordingKitT) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
}

// TestEveryEndpointDeclarationRuleCarriesItsOwnWitness: the condition each
// rule enforces is stated beside its check, as the declaration it refuses, so
// a rule is never written without the input that falsifies it. The witness is
// refused by that rule alone — with the rule deleted it is accepted, or
// refused by another rule with another message — and the shipped kit carries
// a fixture naming the rule with the same message, so what the table states
// and what a consumer is driven through cannot drift apart.
func TestEveryEndpointDeclarationRuleCarriesItsOwnWitness(t *testing.T) {
	all, err := EndpointDeclarationFixtures()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range endpointDeclarationRules() {
		if rule.message == "" {
			t.Errorf("rule %s states no message", rule.name)
			continue
		}
		err := validateEndpointDeclaration(rule.witness, "")
		if err == nil || !strings.Contains(err.Error(), rule.message) {
			t.Errorf("rule %s: its witness is not refused naming %q: %v", rule.name, rule.message, err)
		}
		if err := validateEndpointDeclaration(rule.witness, rule.name); err != nil && strings.Contains(err.Error(), rule.message) {
			t.Errorf("rule %s: its witness is still refused naming %q with the rule deleted: the condition lives elsewhere", rule.name, rule.message)
		}
		shipped := false
		for _, fixture := range all {
			if fixture.Rule == rule.name && fixture.Message == rule.message {
				shipped = true
			}
		}
		if !shipped {
			t.Errorf("rule %s: no shipped fixture carries its witness condition %q", rule.name, rule.message)
		}
	}
}
