package modulecontract

import (
	"embed"
	"errors"
	"fmt"
	"strings"
)

// The fixtures are shipped, not test scaffolding: the CLI derives authority
// documents from these contracts, the runtimes publish contracts of this
// shape, and the host's loader reads them, and every reader is driven through
// the same bytes — so a decoder that started accepting an invalid ceiling, or
// a reader that refused the published shape, fails here and everywhere at
// once. Embedding them makes them reachable from another module, which a
// testdata directory alone is not.
//
//go:embed testdata/*.yaml
var fixtures embed.FS

// Outcome is what a conforming reader must reach for a fixture.
type Outcome string

const (
	// OutcomeAccepted means the contract parses and validates.
	OutcomeAccepted Outcome = "accepted"
	// OutcomeRefused means the contract is refused with the fixture's
	// Sentinel, and a message carrying its Message.
	OutcomeRefused Outcome = "refused"
)

// Fixture is one shipped contract and the outcome every reader must reach.
type Fixture struct {
	// Name is the fixture's stable name.
	Name string
	// Document is the contract, byte for byte.
	Document []byte
	// Outcome is accepted or refused.
	Outcome Outcome
	// Sentinel is the error a refusal must match with errors.Is.
	Sentinel error
	// Message is a substring a refusal's message must carry: the reason,
	// named the same by every reader, so an operator sees one message for one
	// condition.
	Message string
}

// Fixtures returns every shipped contract with its outcome, in a stable order.
// Count it with len rather than trusting a number in prose.
func Fixtures() ([]Fixture, error) {
	table := []struct {
		name, file, message string
		outcome             Outcome
		sentinel            error
	}{
		{name: "valid", file: "valid.module.contract.codefly.yaml", outcome: OutcomeAccepted},
		{name: "upper-case-slot-key", file: "upper-case-slot-key.yaml", outcome: OutcomeAccepted},
		{name: "another-schema", file: "another-schema.yaml", outcome: OutcomeRefused, sentinel: ErrSchema, message: "codefly/module-contract/v2"},
		{name: "tenancy", file: "tenancy.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "tenancy"},
		{name: "literal-audience", file: "literal-audience.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "a slot is {from: <group>/<key>}"},
		{name: "slot-with-default", file: "slot-with-default.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "unknown slot field"},
		{name: "operation-without-ceiling", file: "operation-without-ceiling.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "no scope ceiling"},
		{name: "ceiling-for-undeclared-operation", file: "ceiling-for-undeclared-operation.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "does not declare"},
		{name: "queues-omitted", file: "queues-omitted.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "queues must be declared"},
		{name: "unknown-operation", file: "unknown-operation.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "not one of"},
		{name: "unknown-destination-kind", file: "unknown-destination-kind.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "kind"},
		{name: "duplicate-binding", file: "duplicate-binding.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "declared twice"},
		{name: "audience-from-profile-key", file: "audience-from-profile-key.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "names a key that is not *-audience or *-prefix"},
		{name: "resource-kind-from-endpoint-key", file: "resource-kind-from-endpoint-key.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "names a key that is not *-resource-kind"},
		{name: "binding-key-from-bare-name", file: "binding-key-from-bare-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "names a key that is not *-binding"},
		{name: "bare-actions-without-resource-kind", file: "bare-actions-without-resource-kind.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "lists bare actions but the binding declares no resource_kind slot"},
		{name: "mixed-ceiling", file: "mixed-ceiling.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "mixes {resource_kind, actions} entries with bare actions"},
		{name: "kind-named-twice", file: "kind-named-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `names resource kind "annotations.vocabularies" twice`},
		{name: "kind-with-no-action", file: "kind-with-no-action.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `resource kind "annotations.annotations" permits no action`},
		{name: "ceiling-entry-with-own-field", file: "ceiling-entry-with-own-field.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `unknown scope ceiling field "resource_ids"`},
		{name: "ceiling-not-a-list", file: "ceiling-not-a-list.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "a scope ceiling is a list of actions or of {resource_kind, actions} entries"},
	}
	result := make([]Fixture, 0, len(table))
	for _, entry := range table {
		document, err := fixtures.ReadFile("testdata/" + entry.file)
		if err != nil {
			return nil, fmt.Errorf("module contract fixture %s: %w", entry.name, err)
		}
		result = append(result, Fixture{Name: entry.name, Document: document, Outcome: entry.outcome, Sentinel: entry.sentinel, Message: entry.message})
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
				t.Errorf("module contract fixture %s must be refused with %v", fixture.Name, fixture.Sentinel)
			case !errors.Is(err, fixture.Sentinel):
				t.Errorf("module contract fixture %s must be refused with %v, got: %v", fixture.Name, fixture.Sentinel, err)
			case !strings.Contains(err.Error(), fixture.Message):
				t.Errorf("module contract fixture %s must be refused naming %q, got: %v", fixture.Name, fixture.Message, err)
			}
		}
	}
}
