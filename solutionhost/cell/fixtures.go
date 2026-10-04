package cell

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
	// OutcomeAccepted means the cell parses and validates.
	OutcomeAccepted Outcome = "accepted"
	// OutcomeRefused means the cell is refused with the fixture's Sentinel,
	// and a message carrying its Message.
	OutcomeRefused Outcome = "refused"
)

// Fixture is one document of the conformance kit and the verdict every
// reader must reach on it.
type Fixture struct {
	// Name identifies the fixture in a failure.
	Name string
	// Document is the file's bytes, as a publish would write them.
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
func Fixtures() ([]Fixture, error) {
	table := []struct {
		name, file, message, rule string
		outcome                   Outcome
		sentinel                  error
	}{
		{name: "valid", file: "valid.cell.yaml", outcome: OutcomeAccepted},
		{name: "hostless", file: "hostless.yaml", outcome: OutcomeAccepted},
		{name: "selector-empty-label-value", file: "selector-empty-label-value.yaml", outcome: OutcomeAccepted},

		{name: "not-yaml", file: "not-yaml.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "yaml", rule: ruleWellFormed},
		{name: "another-schema", file: "another-schema.yaml", outcome: OutcomeRefused, sentinel: ErrSchema, message: "codefly/cell/v2", rule: ruleSchema},
		{name: "schema-omitted", file: "schema-omitted.yaml", outcome: OutcomeRefused, sentinel: ErrSchema, message: `"" (this reader reads`, rule: ruleSchema},
		{name: "unknown-field", file: "unknown-field.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "field generation not found", rule: ruleKnownFields},
		{name: "two-documents", file: "two-documents.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "more than one document", rule: ruleOneDocument},

		{name: "environment-not-a-name", file: "environment-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `environment "Production" is not a lowercase name`, rule: ruleEnvironmentName},
		{name: "partial-host-header", file: "partial-host-header.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "the host header is partial", rule: ruleHostHeaderWhole},
		{name: "host-coordinate-not-a-name", file: "host-coordinate-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `host coordinate "Example/Production" is not a lowercase dotted or slashed name`, rule: ruleHostName},
		{name: "trust-domain-malformed", file: "trust-domain-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `trust_domain "Cluster Example" is not a SPIFFE trust domain`, rule: ruleTrustDomain},

		{name: "namespace-not-a-label", file: "namespace-not-a-label.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `namespace "payments.v2" is not a DNS label`, rule: ruleNamespaceName},
		{name: "namespace-module-not-a-name", file: "namespace-module-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `module "Payments" is not a lowercase name`, rule: ruleNamespaceModule},
		{name: "namespace-declared-twice", file: "namespace-declared-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `namespace "payments" is declared twice`, rule: ruleNamespaceUnique},
		{name: "module-in-two-namespaces", file: "module-in-two-namespaces.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "declared in two namespaces", rule: ruleModuleOnce},
		{name: "namespaces-out-of-order", file: "namespaces-out-of-order.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "namespaces are not in name order", rule: ruleNamespacesOrder},
		{name: "workload-declared-twice", file: "workload-declared-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `declares workload Deployment "api" twice`, rule: ruleWorkloadUnique},
		{name: "workloads-out-of-order", file: "workloads-out-of-order.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "workloads are not in name order", rule: ruleWorkloadsOrder},
		{name: "workload-name-not-a-subdomain", file: "workload-name-not-a-subdomain.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `workload name "API" is not a DNS subdomain`, rule: ruleWorkloadName},
		{name: "unknown-workload-kind", file: "unknown-workload-kind.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `kind "ReplicaSet" is not one of`, rule: ruleWorkloadKind},
		{name: "empty-selector", file: "empty-selector.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "carries no selector", rule: ruleSelectorPresent},
		{name: "delivery-without-selector", file: "delivery-without-selector.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "delivery carries no selector", rule: ruleSelectorPresent},
		{name: "selector-label-key-malformed", file: "selector-label-key-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `selector label key "bad/key/x" is not a Kubernetes label key`, rule: ruleSelectorLabelKey},
		{name: "selector-label-value-malformed", file: "selector-label-value-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `selector label app="bad/value" is not a Kubernetes label value`, rule: ruleSelectorLabelVal},
		{name: "service-not-qualified", file: "service-not-qualified.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `service "api" is not <module>/<service>`, rule: ruleServiceQualified},
		{name: "service-of-another-module", file: "service-of-another-module.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "of another module than the namespace's", rule: ruleServiceOfModule},
		{name: "account-not-a-subdomain", file: "account-not-a-subdomain.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `service_account "API_SA" is not a DNS subdomain`, rule: ruleAccountName},
		{name: "spiffe-id-without-trust-domain", file: "spiffe-id-without-trust-domain.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "but the cell declares no trust domain", rule: ruleSPIFFEIDHostless},
		{name: "spiffe-id-of-another-account", file: "spiffe-id-of-another-account.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "the trust domain, namespace and account beside it say", rule: ruleSPIFFEID},
		{name: "delivery-spiffe-id-of-another-account", file: "delivery-spiffe-id-of-another-account.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "delivery spiffe_id is", rule: ruleSPIFFEID},
		{name: "workload-without-container", file: "workload-without-container.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "declares no container", rule: ruleContainersExist},
		{name: "container-name-not-a-label", file: "container-name-not-a-label.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `container name "Proxy" is not a DNS label`, rule: ruleContainerName},
		{name: "container-named-twice", file: "container-named-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `names container "api" twice`, rule: ruleContainerUnique},
		{name: "image-repository-padded", file: "image-repository-padded.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "is not a canonical image repository", rule: ruleImageRepository},
		{name: "image-repository-carries-digest", file: "image-repository-carries-digest.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: reasonTagOrDigest, rule: ruleImageRepository},
		{name: "image-repository-carries-tag", file: "image-repository-carries-tag.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: reasonTagOrDigest, rule: ruleImageRepository},
		{name: "delivery-image-repository-malformed", file: "delivery-image-repository-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `delivery container "deliver" image repository "curl" is not a canonical image repository`, rule: ruleImageRepository},
		{name: "image-without-digest", file: "image-without-digest.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "must pin an OCI manifest digest", rule: ruleImageDigest},
		{name: "image-digest-not-hex", file: "image-digest-not-hex.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "must pin an OCI manifest digest (sha256:<64 hex>)", rule: ruleImageDigest},
		{name: "delivery-image-without-digest", file: "delivery-image-without-digest.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `delivery container "deliver" must pin an OCI manifest digest`, rule: ruleImageDigest},
		{name: "authenticating-not-a-container", file: "authenticating-not-a-container.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "as its authenticating container, which is not one of its containers", rule: ruleAuthenticating},
		{name: "artifact-digest-not-hex", file: "artifact-digest-not-hex.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "the SHA-256 of its rendered bytes (sha256:<64 hex>)", rule: ruleArtifact},
		{name: "release-without-version", file: "release-without-version.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "release must carry publisher, name and version", rule: ruleReleaseWhole},
		{name: "endpoint-not-a-name", file: "endpoint-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `endpoint "GRPC" is not a lowercase name`, rule: ruleEndpointName},
		{name: "endpoint-declared-twice", file: "endpoint-declared-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `endpoint "grpc" is declared twice`, rule: ruleEndpointUnique},
		{name: "endpoints-out-of-order", file: "endpoints-out-of-order.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "endpoints are not in name order", rule: ruleEndpointsOrder},
		{name: "endpoint-port-out-of-range", file: "endpoint-port-out-of-range.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "port 70000 is not a port", rule: ruleEndpointPort},
		{name: "endpoint-visibility-unknown", file: "endpoint-visibility-unknown.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `visibility "internal-only" is not one of`, rule: ruleVisibility},
		{name: "allow-modules-not-a-module", file: "allow-modules-not-a-module.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `allow_modules names "Billing"`, rule: ruleAllowModules},
		{name: "consumer-not-qualified", file: "consumer-not-qualified.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "is not <module>/<service>", rule: ruleConsumerQualify},
		{name: "consumers-out-of-order", file: "consumers-out-of-order.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "consumers are not in order", rule: ruleConsumersOrder},
		{name: "ingress-to-an-endpoint-not-served", file: "ingress-to-an-endpoint-not-served.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "which the workload does not serve", rule: ruleIngressServed},
		{name: "ingress-without-host", file: "ingress-without-host.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "ingress to rest names no host", rule: ruleIngressHasHost},
		{name: "ingress-host-malformed", file: "ingress-host-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `host "pay ments.example.com" is not a host name`, rule: ruleIngressHostName},
		{name: "ingress-out-of-order", file: "ingress-out-of-order.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "ingress is not in endpoint order", rule: ruleIngressOrder},
		{name: "binding-not-a-name", file: "binding-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `binding "Cache" is not a lowercase name`, rule: ruleBindingName},
		{name: "binding-declared-twice", file: "binding-declared-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `binding "cache" is declared twice`, rule: ruleBindingUnique},

		{name: "egress-service-not-qualified", file: "egress-service-not-qualified.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `egress service "api" is not <module>/<service>`, rule: ruleEgressQualified},
		{name: "egress-of-another-module", file: "egress-of-another-module.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `egress for "billing/api" names a service of another module than the namespace's "payments"`, rule: ruleEgressOfModule},
		{name: "egress-without-target", file: "egress-without-target.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "declares neither a host nor a CIDR", rule: ruleEgressHasTarget},
		{name: "egress-host-malformed", file: "egress-host-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `host "pay ments.example.net" is not a host name`, rule: ruleEgressHostName},
		{name: "egress-host-without-port", file: "egress-host-without-port.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "the port is always explicit", rule: ruleEgressHostPort},
		{name: "egress-cidr-malformed", file: "egress-cidr-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "CIDR", rule: ruleEgressCIDR},
		{name: "egress-service-twice", file: "egress-service-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "egress is not in service order, or names a service twice", rule: ruleEgressOrder},

		{name: "delivery-not-a-job", file: "delivery-not-a-job.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "is not a Job", rule: ruleDeliveryKind},
		{name: "delivery-account-malformed", file: "delivery-account-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `delivery service_account "DELIVERY" is not a DNS subdomain`, rule: ruleDeliveryAccount},
		{name: "delivery-container-not-a-label", file: "delivery-container-not-a-label.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `delivery container "Deliver" is not a DNS label`, rule: ruleDeliveryContainer},
	}
	result := make([]Fixture, 0, len(table))
	for _, entry := range table {
		document, err := fixtures.ReadFile("testdata/" + entry.file)
		if err != nil {
			return nil, fmt.Errorf("cell fixture %s: %w", entry.name, err)
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
// actually reads cells with — the loader's parse, the publisher's read —
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
				t.Errorf("cell fixture %s must be refused with %v (rule %s)", fixture.Name, fixture.Sentinel, fixture.Rule)
			case !errors.Is(err, fixture.Sentinel):
				t.Errorf("cell fixture %s must be refused with %v (rule %s), got: %v", fixture.Name, fixture.Sentinel, fixture.Rule, err)
			case !strings.Contains(err.Error(), fixture.Message):
				t.Errorf("cell fixture %s must be refused naming %q (rule %s), got: %v", fixture.Name, fixture.Message, fixture.Rule, err)
			}
		}
	}
}
