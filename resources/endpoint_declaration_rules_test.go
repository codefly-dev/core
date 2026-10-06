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

	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// readWithout reads a service manifest the way the loader does — the endpoint
// decoder on each endpoint mapping, then the loader's endpoint pass — with one
// rule deleted. It is the reader the self-check proves each rule against: the
// same decodeYAML and postLoadEndpoints every real path runs, with a name.
func readWithout(deleted string) func(document []byte) error {
	return func(document []byte) error {
		var root yaml.Node
		if err := yaml.Unmarshal(document, &root); err != nil {
			return err
		}
		if len(root.Content) == 0 {
			return nil
		}
		service := &Service{module: "alpha"}
		for i := 0; i+1 < len(root.Content[0].Content); i += 2 {
			if root.Content[0].Content[i].Value != "endpoints" {
				continue
			}
			for _, entry := range root.Content[0].Content[i+1].Content {
				endpoint := &Endpoint{}
				if err := endpoint.decodeYAML(entry, deleted); err != nil {
					return err
				}
				service.Endpoints = append(service.Endpoints, endpoint)
			}
		}
		service.Name = "alpha"
		return service.postLoadEndpoints(deleted)
	}
}

// readWireWithout reads an endpoint off the wire the way a proto ingress does
// — decode, then judge the declaration whole — with one rule deleted.
func readWireWithout(deleted string) func(raw []byte) error {
	return func(raw []byte) error {
		endpoint := &basev0.Endpoint{}
		if err := proto.Unmarshal(raw, endpoint); err != nil {
			return err
		}
		return validateEndpointDeclaration(EndpointDeclarationOf(endpoint), deleted)
	}
}

// TestEveryEndpointDeclarationRuleIsProtectedByAFixture is the kit's
// self-check: every rule the endpoint model enforces is named by at least one
// refused fixture — of the manifest kit or of the wire kit — every refused
// fixture names a rule that exists, and deleting any one rule fails the kit
// that protects it, on a fixture naming that rule, which the deletion lets
// through or hands to a later rule with another message. A rule this test
// cannot falsify is a rule the kits do not protect.
func TestEveryEndpointDeclarationRuleIsProtectedByAFixture(t *testing.T) {
	manifests, err := EndpointDeclarationFixtures()
	if err != nil {
		t.Fatal(err)
	}
	wires, err := EndpointWireFixtures()
	if err != nil {
		t.Fatal(err)
	}
	names := endpointDeclarationRuleNames()
	protected := map[string]int{}
	for _, fixture := range manifests {
		switch fixture.Outcome {
		case EndpointDeclarationAccepted:
			if fixture.Rule != "" {
				t.Errorf("accepted fixture %s names rule %s", fixture.Name, fixture.Rule)
			}
		case EndpointDeclarationRefused:
			if !slices.Contains(names, fixture.Rule) {
				t.Errorf("refused fixture %s names rule %q, which does not exist", fixture.Name, fixture.Rule)
			}
			protected[fixture.Rule]++
		}
	}
	for _, fixture := range wires {
		if fixture.Outcome == EndpointDeclarationRefused {
			if !slices.Contains(names, fixture.Rule) {
				t.Errorf("refused wire fixture %s names rule %q, which does not exist", fixture.Name, fixture.Rule)
			}
			protected[fixture.Rule]++
		}
	}
	for _, rule := range endpointDeclarationRules() {
		if protected[rule.name] == 0 {
			t.Errorf("rule %s is protected by no fixture: a reader could drop it and pass the kits", rule.name)
			continue
		}
		// The kits, run against this reader with the rule deleted, must fail
		// — the manifest kit for a rule a manifest reaches, the wire kit for
		// the wire rule — and each fixture naming the rule is no longer
		// refused BY it.
		recorder := &recordingKitT{}
		RunEndpointDeclarationKit(recorder, readWithout(rule.name))
		RunEndpointWireKit(recorder, readWireWithout(rule.name))
		if recorder.failures == 0 {
			t.Errorf("rule %s can be deleted and both kits still pass: %d fixture(s) name it but none notices", rule.name, protected[rule.name])
		}
		for _, fixture := range manifests {
			if fixture.Rule != rule.name {
				continue
			}
			err := readWithout(rule.name)(fixture.Document)
			if err != nil && strings.Contains(err.Error(), fixture.Message) {
				t.Errorf("fixture %s is still refused naming %q with rule %s deleted: the refusal comes from somewhere else", fixture.Name, fixture.Message, rule.name)
			}
		}
		for _, fixture := range wires {
			if fixture.Rule != rule.name {
				continue
			}
			err := readWireWithout(rule.name)(fixture.Raw)
			if err != nil && strings.Contains(err.Error(), fixture.Message) {
				t.Errorf("wire fixture %s is still refused naming %q with rule %s deleted: the refusal comes from somewhere else", fixture.Name, fixture.Message, rule.name)
			}
		}
	}
}

// TestEveryEndpointDeclarationRuleCarriesItsOwnWitness: the condition each
// rule enforces is stated beside its check, as the input it refuses — a
// declaration for a declaration rule, a key list for a decoder rule — so a
// rule is never written without the input that falsifies it. The witness is
// refused by that rule alone — with the rule deleted it is accepted, or
// refused by another rule with another message — and a shipped fixture
// carries the same condition, so what the table states and what a consumer is
// driven through cannot drift apart.
func TestEveryEndpointDeclarationRuleCarriesItsOwnWitness(t *testing.T) {
	manifests, err := EndpointDeclarationFixtures()
	if err != nil {
		t.Fatal(err)
	}
	wires, err := EndpointWireFixtures()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range endpointDeclarationRules() {
		if rule.message == "" {
			t.Errorf("rule %s states no message", rule.name)
			continue
		}
		if rule.check == nil && rule.keys == nil {
			t.Errorf("rule %s judges nothing", rule.name)
			continue
		}
		if rule.check != nil {
			err := validateEndpointDeclaration(rule.witness, "")
			if err == nil || !strings.Contains(err.Error(), rule.message) {
				t.Errorf("rule %s: its witness is not refused naming %q: %v", rule.name, rule.message, err)
			}
			if err := validateEndpointDeclaration(rule.witness, rule.name); err != nil && strings.Contains(err.Error(), rule.message) {
				t.Errorf("rule %s: its witness is still refused naming %q with the rule deleted: the condition lives elsewhere", rule.name, rule.message)
			}
		}
		if rule.keys != nil {
			err := validateEndpointKeys("grpc", rule.witnessKeys, "")
			if err == nil || !strings.Contains(err.Error(), rule.message) {
				t.Errorf("rule %s: its key witness %v is not refused naming %q: %v", rule.name, rule.witnessKeys, rule.message, err)
			}
			if err := validateEndpointKeys("grpc", rule.witnessKeys, rule.name); err != nil && strings.Contains(err.Error(), rule.message) {
				t.Errorf("rule %s: its key witness is still refused naming %q with the rule deleted: the condition lives elsewhere", rule.name, rule.message)
			}
		}
		shipped := false
		for _, fixture := range manifests {
			if fixture.Rule == rule.name && strings.Contains(fixture.Message, rule.message) {
				shipped = true
			}
		}
		for _, fixture := range wires {
			if fixture.Rule == rule.name && strings.Contains(fixture.Message, rule.message) {
				shipped = true
			}
		}
		if !shipped {
			t.Errorf("rule %s: no shipped fixture carries its witness condition %q", rule.name, rule.message)
		}
	}
}

// TestEveryEndpointDeclarationFixtureReachesItsOutcome runs the manifest kit
// against the real loader — a manifest on disk, read by LoadServiceFromDir,
// the way every consumer reads one — and holds the kit to its exact contents:
// a fixture that drifted from the loader, or one that was dropped, is caught
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
		"allow-modules-camel", "allow-modules-empty", "allow-modules-named",
		"allow-modules-null", "allow-modules-on-public", "allow-modules-underscore",
		"allow-modules-wildcard", "endpoint-key-unknown", "exposed-public",
		"exposure-on-external", "exposure-on-internal", "exposure-on-private",
		"exposure-unknown", "external-private", "location-unknown", "valid",
		"visibility-external", "visibility-module", "visibility-unknown",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("the kit ships %v; a changed kit is a changed rule set, declared here", names)
	}
}

// TestEveryEndpointWireFixtureReachesItsOutcome runs the wire kit against the
// real proto ingresses: the projection into the resource model
// (FromProtoEndpoints) and the network allocation (the runtime manager, in
// its own package's test). Here, the projection and the dependency verdict.
func TestEveryEndpointWireFixtureReachesItsOutcome(t *testing.T) {
	RunEndpointWireKit(t, func(raw []byte) error {
		endpoint := &basev0.Endpoint{}
		if err := proto.Unmarshal(raw, endpoint); err != nil {
			return err
		}
		_, err := FromProtoEndpoints(endpoint)
		return err
	})
	RunEndpointWireKit(t, func(raw []byte) error {
		endpoint := &basev0.Endpoint{}
		if err := proto.Unmarshal(raw, endpoint); err != nil {
			return err
		}
		_, err := ConsumedDependencyEndpoints("billing", &ServiceDependency{Module: "saas", Name: "accounts"}, []*basev0.Endpoint{endpoint})
		return err
	})
	RunInterfaceEndpointWireKit(t, func(raw []byte) error {
		entry := &basev0.InterfaceEndpoint{}
		if err := proto.Unmarshal(raw, entry); err != nil {
			return err
		}
		return ValidateInterfaceEndpointWire(entry)
	})
}

// TestTheEndpointDeclarationKitFailsAReaderThatSkipsARule: a reader that
// decodes but does not judge the declaration passes the accepted fixtures and
// fails the refusals by name, which is what makes the kit a gate rather than
// a round-trip; and a wire reader that projects without judging fails the
// wire kit by name.
func TestTheEndpointDeclarationKitFailsAReaderThatSkipsARule(t *testing.T) {
	recorder := &recordingKitT{}
	RunEndpointDeclarationKit(recorder, func(document []byte) error {
		_, err := LoadFromBytes[Service](document)
		return err
	})
	if recorder.failures == 0 || !strings.Contains(recorder.messages, "fixture visibility-module must be refused") {
		t.Fatalf("the kit did not name the rule the reader skipped:\n%s", recorder.messages)
	}
	wire := &recordingKitT{}
	RunEndpointWireKit(wire, func(raw []byte) error {
		endpoint := &basev0.Endpoint{}
		if err := proto.Unmarshal(raw, endpoint); err != nil {
			return err
		}
		_ = EndpointFromProto(endpoint)
		return nil
	})
	if wire.failures == 0 || !strings.Contains(wire.messages, "wire fixture allow-modules-field-9 must be refused") {
		t.Fatalf("the wire kit did not name the field the reader carried past:\n%s", wire.messages)
	}
}

// TestEveryEndpointDeclarationRuleHoldsExactlyOneCondition enumerates the
// refusals in each rule's check from the package's own source: a second
// fmt.Errorf added inside an existing rule would be a condition no fixture is
// required to protect, and it fails at that function. Every judging function
// the table references is one of these, so a condition cannot be written in a
// function that is no rule's.
func TestEveryEndpointDeclarationRuleHoldsExactlyOneCondition(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "endpoint_declaration.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	conditions := map[string]int{}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || !(strings.HasPrefix(function.Name.Name, "check") || strings.HasPrefix(function.Name.Name, "keys")) {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "fmt" && selector.Sel.Name == "Errorf" {
					conditions[function.Name.Name]++
				}
			}
			return true
		})
	}
	judging := 0
	for _, rule := range endpointDeclarationRules() {
		if rule.check != nil {
			judging++
		}
		if rule.keys != nil {
			judging++
		}
	}
	if len(conditions) != judging {
		t.Fatalf("%d judging functions in the source for %d judgements in the table", len(conditions), judging)
	}
	for name, count := range conditions {
		if count != 1 {
			t.Errorf("%s holds %d refusals; a rule holds exactly one condition, so every condition has a fixture", name, count)
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
