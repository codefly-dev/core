package resources

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// WireFixture is one encoded message of the wire kit and the verdict every
// proto ingress must reach on it. The YAML kit proves a reader refuses what
// the model refuses when a manifest is read; this kit proves the same at the
// other ingress — bytes a peer sent — where protobuf keeps a field the schema
// does not define as unknown data instead of refusing it, so an authored
// allow-list on the reserved number would otherwise ride in unseen.
type WireFixture struct {
	// Name identifies the fixture in a failure.
	Name string
	// Raw is the message's bytes, as a peer would send them.
	Raw []byte
	// Outcome is the verdict.
	Outcome EndpointDeclarationOutcome
	// Message is text a refusal must carry.
	Message string
	// Rule names the rule a refused fixture protects.
	Rule string
}

// wireFixtureCase is how a wire fixture is stated: a valid message, plus the
// raw fields appended to it — so every refused fixture is the accepted one
// with ONE addition, and what the rule refuses is exactly that addition.
type wireFixtureCase struct {
	name, message, rule string
	outcome             EndpointDeclarationOutcome
	appended            []byte
}

func wireFixtures(valid proto.Message, cases []wireFixtureCase, kind string) ([]WireFixture, error) {
	base, err := proto.MarshalOptions{Deterministic: true}.Marshal(valid)
	if err != nil {
		return nil, fmt.Errorf("%s wire fixture: %w", kind, err)
	}
	result := make([]WireFixture, 0, len(cases))
	for _, entry := range cases {
		raw := append(append([]byte(nil), base...), entry.appended...)
		result = append(result, WireFixture{Name: entry.name, Raw: raw, Outcome: entry.outcome, Message: entry.message, Rule: entry.rule})
	}
	return result, nil
}

// EndpointWireFixtures is the wire kit for codefly.base.v0.Endpoint: a valid
// internal endpoint, the same bytes with the reserved allow_modules field (9)
// carrying a module, with the reserved field carrying the wildcard, and with
// a field no schema version ever defined.
func EndpointWireFixtures() ([]WireFixture, error) {
	valid := &basev0.Endpoint{Name: "grpc", Service: "accounts", Module: "saas", Api: "grpc", Visibility: VisibilityInternal}
	return wireFixtures(valid, []wireFixtureCase{
		{name: "valid", outcome: EndpointDeclarationAccepted},
		{name: "allow-modules-field-9", outcome: EndpointDeclarationRefused, rule: ruleWireFieldsKnown, message: "carries unknown wire fields [9]",
			appended: protowire.AppendString(protowire.AppendTag(nil, 9, protowire.BytesType), "billing")},
		{name: "allow-modules-field-9-wildcard", outcome: EndpointDeclarationRefused, rule: ruleWireFieldsKnown, message: "carries unknown wire fields [9]",
			appended: protowire.AppendString(protowire.AppendTag(nil, 9, protowire.BytesType), "*")},
		{name: "unknown-field-99", outcome: EndpointDeclarationRefused, rule: ruleWireFieldsKnown, message: "carries unknown wire fields [99]",
			appended: protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1)},
	}, "endpoint")
}

// InterfaceEndpointWireFixtures is the wire kit for
// codefly.base.v0.InterfaceEndpoint: a valid internal export, and the same
// bytes with the reserved allow_modules field (4) carrying a module.
func InterfaceEndpointWireFixtures() ([]WireFixture, error) {
	valid := &basev0.InterfaceEndpoint{Service: "accounts", Endpoint: "grpc", Visibility: VisibilityInternal}
	return wireFixtures(valid, []wireFixtureCase{
		{name: "valid", outcome: EndpointDeclarationAccepted},
		{name: "allow-modules-field-4", outcome: EndpointDeclarationRefused, rule: ruleWireFieldsKnown, message: "carries unknown wire fields [4]",
			appended: protowire.AppendString(protowire.AppendTag(nil, 4, protowire.BytesType), "billing")},
		{name: "unknown-field-99", outcome: EndpointDeclarationRefused, rule: ruleWireFieldsKnown, message: "carries unknown wire fields [99]",
			appended: protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1)},
	}, "interface endpoint")
}

// ValidateInterfaceEndpointWire judges an interface entry read from the wire:
// it carries only the fields its schema defines — the reserved allow_modules
// (4) is refused by number — and a reach an export can carry. It is the proto
// counterpart of InterfaceEndpoint's YAML decoder, for the consumer that reads
// a published module.
func ValidateInterfaceEndpointWire(ie *basev0.InterfaceEndpoint) error {
	if ie == nil {
		return fmt.Errorf("%w: interface endpoint is nil", ErrInvalidEndpointDeclaration)
	}
	if unknown := UnknownWireFields(ie.ProtoReflect().GetUnknown()); len(unknown) > 0 {
		return fmt.Errorf("%w: interface endpoint %s/%s carries unknown wire fields %v: the schema defines no such field, and allow_modules (4) — an authored allow-list — is reserved, not read",
			ErrInvalidEndpointDeclaration, ie.GetService(), ie.GetEndpoint(), unknown)
	}
	entry := &InterfaceEndpoint{Service: ie.GetService(), Endpoint: ie.GetEndpoint(), Visibility: ie.GetVisibility()}
	if err := entry.validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidEndpointDeclaration, err)
	}
	return nil
}

// runWireKit drives a reader's proto ingress through every fixture and fails
// the test on the first outcome that differs.
func runWireKit(t KitTestingT, kind string, fixtures []WireFixture, read func(raw []byte) error) {
	t.Helper()
	if read == nil {
		t.Fatalf("%s wire conformance: no entrypoint given", kind)
		return
	}
	for _, fixture := range fixtures {
		err := read(fixture.Raw)
		switch fixture.Outcome {
		case EndpointDeclarationAccepted:
			if err != nil {
				t.Errorf("%s wire fixture %s must be accepted, got: %v", kind, fixture.Name, err)
			}
		case EndpointDeclarationRefused:
			switch {
			case err == nil:
				t.Errorf("%s wire fixture %s must be refused with %v (rule %s)", kind, fixture.Name, ErrInvalidEndpointDeclaration, fixture.Rule)
			case !errors.Is(err, ErrInvalidEndpointDeclaration):
				t.Errorf("%s wire fixture %s must be refused with %v (rule %s), got: %v", kind, fixture.Name, ErrInvalidEndpointDeclaration, fixture.Rule, err)
			case !strings.Contains(err.Error(), fixture.Message):
				t.Errorf("%s wire fixture %s must be refused naming %q (rule %s), got: %v", kind, fixture.Name, fixture.Message, fixture.Rule, err)
			}
		}
	}
}

// RunEndpointWireKit drives a reader's endpoint ingress — the function that
// takes bytes a peer sent and produces, judges or allocates from an endpoint —
// through every wire fixture. A consumer passes its own ingress: a decode
// followed by whatever it does with the endpoint, never
// ValidateEndpointDeclaration itself.
func RunEndpointWireKit(t KitTestingT, read func(raw []byte) error) {
	t.Helper()
	fixtures, err := EndpointWireFixtures()
	if err != nil {
		t.Fatalf("endpoint wire conformance: %v", err)
		return
	}
	runWireKit(t, "endpoint", fixtures, read)
}

// RunInterfaceEndpointWireKit drives a reader's interface-entry ingress
// through every wire fixture.
func RunInterfaceEndpointWireKit(t KitTestingT, read func(raw []byte) error) {
	t.Helper()
	fixtures, err := InterfaceEndpointWireFixtures()
	if err != nil {
		t.Fatalf("interface endpoint wire conformance: %v", err)
		return
	}
	runWireKit(t, "interface endpoint", fixtures, read)
}
