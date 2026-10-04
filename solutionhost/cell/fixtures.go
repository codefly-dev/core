package cell

import (
	"embed"
	"errors"
	"fmt"
	"strings"
)

// The fixtures are shipped, not test scaffolding: the CLI's publish writes
// cells of this shape and the platform's loader reads them, and both are
// driven through the same bytes — so a reader that started accepting a
// workload the platform cannot police, or a writer that produced one, fails
// here and everywhere at once. Embedding them makes them reachable from
// another module, which a testdata directory alone is not.
//
//go:embed testdata/*.yaml
var fixtures embed.FS

// Outcome is what a conforming reader must reach for a fixture.
type Outcome string

const (
	// OutcomeAccepted means the cell parses and validates.
	OutcomeAccepted Outcome = "accepted"
	// OutcomeRefused means the cell is refused with the fixture's Sentinel,
	// and a message carrying its Message.
	OutcomeRefused Outcome = "refused"
)

// Fixture is one shipped cell and the outcome every reader must reach.
type Fixture struct {
	// Name is the fixture's stable name.
	Name string
	// Document is the cell, byte for byte.
	Document []byte
	// Outcome is accepted or refused.
	Outcome Outcome
	// Sentinel is the error a refusal must match with errors.Is.
	Sentinel error
	// Message is a substring a refusal's message must carry.
	Message string
}

// Fixtures returns every shipped cell with its outcome, in a stable order.
// Count it with len rather than trusting a number in prose.
func Fixtures() ([]Fixture, error) {
	table := []struct {
		name, file, message string
		outcome             Outcome
		sentinel            error
	}{
		{name: "valid", file: "valid.cell.yaml", outcome: OutcomeAccepted},
		{name: "hostless", file: "hostless.yaml", outcome: OutcomeAccepted},
		{name: "another-schema", file: "another-schema.yaml", outcome: OutcomeRefused, sentinel: ErrSchema, message: "codefly/cell/v2"},
		{name: "unknown-field", file: "unknown-field.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "field generation not found"},
		{name: "partial-host-header", file: "partial-host-header.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "the host header is partial"},
		{name: "namespaces-out-of-order", file: "namespaces-out-of-order.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "not in name order"},
		{name: "module-in-two-namespaces", file: "module-in-two-namespaces.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "declared in two namespaces"},
		{name: "unknown-workload-kind", file: "unknown-workload-kind.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `kind "ReplicaSet" is not one of`},
		{name: "empty-selector", file: "empty-selector.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "carries no selector"},
		{name: "service-of-another-module", file: "service-of-another-module.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "of another module than the namespace's"},
		{name: "spiffe-id-of-another-account", file: "spiffe-id-of-another-account.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "the trust domain, namespace and account beside it say"},
		{name: "authenticating-not-a-container", file: "authenticating-not-a-container.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "as its authenticating container, which is not one of its containers"},
		{name: "image-without-digest", file: "image-without-digest.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "must pin a repository and an OCI manifest digest"},
		{name: "artifact-digest-not-hex", file: "artifact-digest-not-hex.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "the SHA-256 of its rendered bytes (sha256:<64 hex>)"},
		{name: "ingress-to-an-endpoint-not-served", file: "ingress-to-an-endpoint-not-served.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "which the workload does not serve"},
		{name: "consumer-not-qualified", file: "consumer-not-qualified.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "is not <module>/<service>"},
		{name: "egress-host-without-port", file: "egress-host-without-port.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "the port is always explicit"},
		{name: "egress-cidr-malformed", file: "egress-cidr-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "CIDR"},
		{name: "delivery-not-a-job", file: "delivery-not-a-job.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "is not a Job"},
		{name: "two-documents", file: "two-documents.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "more than one document"},
	}
	result := make([]Fixture, 0, len(table))
	for _, entry := range table {
		document, err := fixtures.ReadFile("testdata/" + entry.file)
		if err != nil {
			return nil, fmt.Errorf("cell fixture %s: %w", entry.name, err)
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
// actually reads cells with — the publisher's merge, the loader's parse —
// never Parse itself, which proves nothing about the consumer.
func Run(t TestingT, read func(document []byte) error) {
	t.Helper()
	if read == nil {
		t.Fatalf("cell conformance: no entrypoint given")
		return
	}
	all, err := Fixtures()
	if err != nil {
		t.Fatalf("cell conformance: %v", err)
		return
	}
	for _, fixture := range all {
		err := read(fixture.Document)
		switch fixture.Outcome {
		case OutcomeAccepted:
			if err != nil {
				t.Errorf("cell fixture %s must be accepted, got: %v", fixture.Name, err)
			}
		case OutcomeRefused:
			switch {
			case err == nil:
				t.Errorf("cell fixture %s must be refused with %v", fixture.Name, fixture.Sentinel)
			case !errors.Is(err, fixture.Sentinel):
				t.Errorf("cell fixture %s must be refused with %v, got: %v", fixture.Name, fixture.Sentinel, err)
			case !strings.Contains(err.Error(), fixture.Message):
				t.Errorf("cell fixture %s must be refused naming %q, got: %v", fixture.Name, fixture.Message, err)
			}
		}
	}
}
