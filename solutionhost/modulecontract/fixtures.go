package modulecontract

import (
	"embed"
	"errors"
	"fmt"
	"strings"
)

//go:embed testdata/*.yaml
var fixtures embed.FS

// Outcome is what a reader must do with a fixture.
type Outcome string

const (
	// OutcomeAccepted means the contract parses and validates.
	OutcomeAccepted Outcome = "accepted"
	// OutcomeRefused means the contract is refused with the fixture's
	// Sentinel, and a message carrying its Message.
	OutcomeRefused Outcome = "refused"
)

// Fixture is one document of the conformance kit and the verdict every
// reader must reach on it.
type Fixture struct {
	// Name identifies the fixture in a failure.
	Name string
	// Document is the file's bytes, as a module would publish them.
	Document []byte
	// Outcome is the verdict.
	Outcome Outcome
	// Sentinel is the error a refusal must match with errors.Is.
	Sentinel error
	// Message is text a refusal must carry: one reason for one condition,
	// whichever reader refused it.
	Message string
	// Rule names the rule a refused fixture protects (rules.go): the one a
	// reader cannot drop without this fixture noticing. Empty for an
	// accepted fixture.
	Rule string
}

// Fixtures is the kit: every accepted and refused document, with the verdict
// each must reach. Every rule the reader enforces is protected by at least
// one refused fixture here, which the package's own tests prove by deleting
// each rule in turn.
// malformedEntry is the one message every malformed-ceiling-entry fixture
// expects.
const malformedEntry = "ceiling entry is malformed"

// explicitNull is the one message every null fixture expects.
const explicitNull = "explicit null"

func Fixtures() ([]Fixture, error) {
	table := []struct {
		name, file, message, rule string
		outcome                   Outcome
		sentinel                  error
	}{
		{name: "valid", file: "valid.module.contract.codefly.yaml", outcome: OutcomeAccepted},
		{name: "upper-case-slot-key", file: "upper-case-slot-key.yaml", outcome: OutcomeAccepted},

		{name: "not-yaml", file: "not-yaml.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "yaml", rule: ruleWellFormed},
		{name: "slot-names-from-twice", file: "slot-names-from-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `names the key "from" twice`, rule: ruleMappingKeysOnce},
		{name: "slot-with-empty-field-name", file: "slot-with-empty-field-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "a key that is not a name", rule: ruleKeyIsAName},
		{name: "ceiling-entry-with-empty-field-name", file: "ceiling-entry-with-empty-field-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "a key that is not a name", rule: ruleKeyIsAName},
		{name: "ceiling-entry-names-a-field-twice", file: "ceiling-entry-names-a-field-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `names the key "resource_kind" twice`, rule: ruleMappingKeysOnce},
		{name: "binding-action-not-a-name", file: "binding-action-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `binding model invoke action "Read"`, rule: ruleActionName},
		{name: "binding-action-declared-twice", file: "binding-action-declared-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `binding model invoke action "read" is declared twice`, rule: ruleActionUnique},
		{name: "slot-reference-tagged", file: "slot-reference-tagged.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "slot reference is malformed", rule: ruleSlotDecodes},
		{name: "bare-action-tagged", file: "bare-action-tagged.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: malformedEntry, rule: ruleCeilingEntryShape},
		{name: "slot-alias-key", file: "slot-alias-key.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `unknown slot field "assistant"`, rule: ruleSlotCarriesOnlyFrom},
		{name: "ceiling-entry-alias-key", file: "ceiling-entry-alias-key.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `unknown scope ceiling field "assistant"`, rule: ruleCeilingEntryFields},
		{name: "queue-not-a-name", file: "queue-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `queues entry "Jobs"`, rule: ruleListEntryName},
		{name: "queue-declared-twice", file: "queue-declared-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `queues entry "jobs" is declared twice`, rule: ruleListEntryUnique},
		{name: "destination-endpoint-not-a-name", file: "destination-endpoint-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `endpoint "HTTP"`, rule: ruleDestinationTargetName},
		{name: "slot-group-not-a-name", file: "slot-group-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `slot "Assistant/model-audience" is not <group>/<key>`, rule: ruleSlotReference},
		{name: "slot-key-not-a-key", file: "slot-key-not-a-key.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `slot "assistant/model.audience" is not <group>/<key>`, rule: ruleSlotReference},
		{name: "resource-kind-literal", file: "resource-kind-literal.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "resource_kind slot is {from: <group>/<key>}", rule: ruleSlotIsAReference},
		{name: "resource-kind-slot-with-default", file: "resource-kind-slot-with-default.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "resource_kind slot carries the unknown slot field", rule: ruleSlotCarriesOnlyFrom},
		{name: "binding-key-literal", file: "binding-key-literal.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "binding_key slot is {from: <group>/<key>}", rule: ruleSlotIsAReference},
		{name: "binding-key-slot-with-default", file: "binding-key-slot-with-default.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "binding_key slot carries the unknown slot field", rule: ruleSlotCarriesOnlyFrom},
		{name: "action-wildcard", file: "action-wildcard.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `action "*"`, rule: ruleActionName},
		{name: "ceiling-entry-nested-list", file: "ceiling-entry-nested-list.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: malformedEntry, rule: ruleCeilingEntryShape},
		{name: "ceiling-operation-named-twice", file: "ceiling-operation-named-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `names the key "invoke" twice`, rule: ruleMappingKeysOnce},
		{name: "operation-admin", file: "operation-admin.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `operation "admin" is not one of`, rule: ruleOperationKnown},
		{name: "destination-kind-cluster-scoped", file: "destination-kind-cluster-scoped.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `kind "cluster-scoped" is not one of`, rule: ruleDestinationKindKnown},
		{name: "binding-key-slot-key-malformed", file: "binding-key-slot-key-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `slot "assistant/bad.key-binding" is not <group>/<key>`, rule: ruleSlotReference},
		{name: "null-key-hiding-a-subtree", file: "null-key-hiding-a-subtree.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: explicitNull, rule: ruleNoNulls},
		{name: "null-in-an-explicit-scope-action", file: "null-in-an-explicit-scope-action.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: explicitNull, rule: ruleNoNulls},
		{name: "null-in-a-bare-ceiling-action", file: "null-in-a-bare-ceiling-action.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: explicitNull, rule: ruleNoNulls},
		{name: "explicit-scope-action-not-a-name", file: "explicit-scope-action-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `action "Write"`, rule: ruleActionName},
		{name: "explicit-scope-action-declared-twice", file: "explicit-scope-action-declared-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `action "write" is declared twice`, rule: ruleActionUnique},
		{name: "another-schema", file: "another-schema.yaml", outcome: OutcomeRefused, sentinel: ErrSchema, message: "codefly/module-contract/v2", rule: ruleSchema},
		{name: "schema-omitted", file: "schema-omitted.yaml", outcome: OutcomeRefused, sentinel: ErrSchema, message: `"" (this reader reads`, rule: ruleSchema},
		{name: "tenancy", file: "tenancy.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "tenancy", rule: ruleKnownFields},
		{name: "two-documents", file: "two-documents.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "more than one document", rule: ruleOneDocument},

		{name: "principal-upper-case", file: "principal-upper-case.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `principal "Assistant" is not a lowercase name`, rule: rulePrincipal},
		{name: "namespaces-omitted", file: "namespaces-omitted.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "namespaces must be declared", rule: ruleListsDeclared},
		{name: "queues-omitted", file: "queues-omitted.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "queues must be declared", rule: ruleListsDeclared},
		{name: "namespace-not-a-name", file: "namespace-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `namespaces entry "Assistant Space" is not a lowercase name`, rule: ruleListEntryName},
		{name: "namespace-declared-twice", file: "namespace-declared-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `namespaces entry "assistant" is declared twice`, rule: ruleListEntryUnique},

		{name: "scope-ceiling-kind-not-a-name", file: "scope-ceiling-kind-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `scope ceiling resource kind "Assistant.Tasks" is not a lowercase name`, rule: ruleCeilingKindName},
		{name: "scope-ceiling-kind-declared-twice", file: "scope-ceiling-kind-declared-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `scope ceiling "assistant.tasks" is declared twice`, rule: ruleCeilingKindUnique},
		{name: "scope-ceiling-without-action", file: "scope-ceiling-without-action.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `scope ceiling "assistant.tasks" contributes no action`, rule: ruleCeilingHasAction},
		{name: "action-not-a-name", file: "action-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `action "Execute" is not a lowercase action name`, rule: ruleActionName},
		{name: "action-declared-twice", file: "action-declared-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `action "read" is declared twice`, rule: ruleActionUnique},

		{name: "binding-id-not-a-name", file: "binding-id-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `binding id "Model" is not a lowercase name`, rule: ruleBindingIDName},
		{name: "duplicate-binding", file: "duplicate-binding.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `binding "model" is declared twice`, rule: ruleBindingIDUnique},
		{name: "binding-without-operation", file: "binding-without-operation.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `binding "evidence" declares no operation`, rule: ruleBindingHasOperation},
		{name: "unknown-operation", file: "unknown-operation.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "not one of", rule: ruleOperationKnown},
		{name: "operation-declared-twice", file: "operation-declared-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `binding "model" declares operation "invoke" twice`, rule: ruleOperationUnique},
		{name: "lookup-method-without-lookup", file: "lookup-method-without-lookup.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `binding "evidence" declares a lookup method without the lookup operation`, rule: ruleLookupNeedsOperation},
		{name: "lookup-method-not-a-name", file: "lookup-method-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `lookup method "Profile" is not a lowercase name`, rule: ruleLookupMethodName},

		{name: "literal-audience", file: "literal-audience.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "slot is {from: <group>/<key>}, not the literal", rule: ruleSlotIsAReference},
		{name: "slot-with-default", file: "slot-with-default.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "unknown slot field", rule: ruleSlotCarriesOnlyFrom},
		{name: "slot-without-group", file: "slot-without-group.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `slot "model-audience" is not <group>/<key>`, rule: ruleSlotReference},
		{name: "audience-from-profile-key", file: "audience-from-profile-key.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "names a key that is not *-audience or *-prefix", rule: ruleSlotKeyMeaning},
		{name: "resource-kind-from-endpoint-key", file: "resource-kind-from-endpoint-key.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "names a key that is not *-resource-kind", rule: ruleSlotKeyMeaning},
		{name: "binding-key-from-bare-name", file: "binding-key-from-bare-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "names a key that is not *-binding", rule: ruleSlotKeyMeaning},

		{name: "ceiling-not-a-list", file: "ceiling-not-a-list.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "a scope ceiling is a list of actions or of {resource_kind, actions} entries", rule: ruleCeilingIsAList},
		{name: "ceiling-entry-with-own-field", file: "ceiling-entry-with-own-field.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `unknown scope ceiling field "resource_ids"`, rule: ruleCeilingEntryFields},
		{name: "ceiling-entry-actions-not-a-list", file: "ceiling-entry-actions-not-a-list.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: malformedEntry, rule: ruleCeilingEntryShape},
		{name: "mixed-ceiling", file: "mixed-ceiling.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "mixes bare actions with {resource_kind, actions} entries", rule: ruleCeilingOneSpelling},
		{name: "ceiling-for-undeclared-operation", file: "ceiling-for-undeclared-operation.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "does not declare", rule: ruleCeilingForDeclaredOp},
		{name: "operation-without-ceiling", file: "operation-without-ceiling.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "no scope ceiling", rule: ruleOperationHasCeiling},
		{name: "bare-actions-without-resource-kind", file: "bare-actions-without-resource-kind.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "lists bare actions but the binding declares no resource_kind slot", rule: ruleBareActionsNeedKind},
		{name: "scope-kind-not-a-name", file: "scope-kind-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `ceiling resource kind "Annotations/Vocabularies" is not a lowercase name`, rule: ruleScopeKindName},
		{name: "kind-named-twice", file: "kind-named-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `names resource kind "annotations.vocabularies" twice`, rule: ruleScopeKindUnique},
		{name: "kind-with-no-action", file: "kind-with-no-action.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `resource kind "annotations.annotations" permits no action`, rule: ruleScopeHasAction},

		{name: "destination-id-not-a-name", file: "destination-id-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `destination id "Chat HTTP" is not a lowercase name`, rule: ruleDestinationIDName},
		{name: "destination-declared-twice", file: "destination-declared-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `destination "chat-http" is declared twice`, rule: ruleDestinationIDUnique},
		{name: "destination-service-not-a-name", file: "destination-service-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `destination "chat-http" service "Chat Service" is not a lowercase name`, rule: ruleDestinationTargetName},
		{name: "unknown-destination-kind", file: "unknown-destination-kind.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "kind", rule: ruleDestinationKindKnown},
	}
	result := make([]Fixture, 0, len(table))
	for _, entry := range table {
		document, err := fixtures.ReadFile("testdata/" + entry.file)
		if err != nil {
			return nil, fmt.Errorf("module contract fixture %s: %w", entry.name, err)
		}
		result = append(result, Fixture{Name: entry.name, Document: document, Outcome: entry.outcome, Sentinel: entry.sentinel, Message: entry.message, Rule: entry.rule})
	}
	return result, nil
}

// TestingT is the part of *testing.T the kit uses, so importing this package
// does not pull the testing flag set into a consumer's binary.
type TestingT interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// Run drives a reader's entrypoint through every fixture and fails the test
// on the first outcome that differs. A consumer passes the function it
// actually reads contracts with — the renderer's load, the loader's parse —
// never Parse itself, which proves nothing about the consumer.
func Run(t TestingT, read func(document []byte) error) {
	t.Helper()
	if read == nil {
		t.Fatalf("module contract conformance: no entrypoint given")
		return
	}
	all, err := Fixtures()
	if err != nil {
		t.Fatalf("module contract conformance: %v", err)
		return
	}
	for _, fixture := range all {
		err := read(fixture.Document)
		switch fixture.Outcome {
		case OutcomeAccepted:
			if err != nil {
				t.Errorf("module contract fixture %s must be accepted, got: %v", fixture.Name, err)
			}
		case OutcomeRefused:
			switch {
			case err == nil:
				t.Errorf("module contract fixture %s must be refused with %v (rule %s)", fixture.Name, fixture.Sentinel, fixture.Rule)
			case !errors.Is(err, fixture.Sentinel):
				t.Errorf("module contract fixture %s must be refused with %v (rule %s), got: %v", fixture.Name, fixture.Sentinel, fixture.Rule, err)
			case !strings.Contains(err.Error(), fixture.Message):
				t.Errorf("module contract fixture %s must be refused naming %q (rule %s), got: %v", fixture.Name, fixture.Message, fixture.Rule, err)
			}
		}
	}
}
