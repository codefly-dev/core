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
// digestRefusal is the one message every image-digest fixture expects.
const digestRefusal = "must pin an OCI manifest digest"

// notWhole is the one message every fractional-number fixture expects.
// explicitNull is the one message every null fixture expects.
const explicitNull = "explicit null"

// overlappingCIDRs is the one message every overlap fixture expects.
const overlappingCIDRs = "overlapping CIDRs"

// notCanonical is the one message every non-canonical-CIDR fixture expects.
const notCanonical = "is not canonical"

const notWhole = "is not a whole number"

// selectorKeyRefusal is the one message every selector-key fixture expects.
const selectorKeyRefusal = "selector label key"

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
		{name: "init-container-name-not-a-label", file: "init-container-name-not-a-label.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `init container name "Migrate"`, rule: ruleContainerName},
		{name: "init-container-image-without-digest", file: "init-container-image-without-digest.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "init container", rule: ruleImageDigest},
		{name: "image-digest-too-short", file: "image-digest-too-short.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: digestRefusal, rule: ruleImageDigest},
		{name: "artifact-digest-too-short", file: "artifact-digest-too-short.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "the SHA-256 of its rendered bytes", rule: ruleArtifact},
		{name: "spiffe-id-of-another-trust-domain", file: "spiffe-id-of-another-trust-domain.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "spiffe://other.example", rule: ruleSPIFFEID},
		{name: "delivery-selector-label-malformed", file: "delivery-selector-label-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `delivery selector label codefly.dev/delivery="bad/value"`, rule: ruleSelectorLabelVal},
		{name: "image-digest-without-prefix", file: "image-digest-without-prefix.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: digestRefusal, rule: ruleImageDigest},
		{name: "release-without-publisher", file: "release-without-publisher.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "publisher, name and version", rule: ruleReleaseWhole},
		{name: "release-without-name", file: "release-without-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "publisher, name and version", rule: ruleReleaseWhole},
		{name: "egress-host-port-out-of-range", file: "egress-host-port-out-of-range.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "port 70000", rule: ruleEgressHostPort},
		{name: "authenticating-is-an-init-container", file: "authenticating-is-an-init-container.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "which is not one of its containers", rule: ruleAuthenticating},
		{name: "consumer-named-twice", file: "consumer-named-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `names consumer "billing/worker" twice`, rule: ruleConsumerUnique},
		{name: "ingress-to-one-endpoint-twice", file: "ingress-to-one-endpoint-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `ingress to endpoint "rest" twice`, rule: ruleIngressOnce},
		{name: "ingress-host-named-twice", file: "ingress-host-named-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "names host \"payments.example.com\" twice", rule: ruleIngressHostOnce},
		{name: "egress-cidr-not-canonical", file: "egress-cidr-not-canonical.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: notCanonical, rule: ruleEgressCIDRShape},
		{name: "egress-cidrs-overlap", file: "egress-cidrs-overlap.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: overlappingCIDRs, rule: ruleEgressCIDROver},
		{name: "spiffe-id-of-another-namespace", file: "spiffe-id-of-another-namespace.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "ns/billing/sa/api", rule: ruleSPIFFEID},
		{name: "artifact-name-not-a-name", file: "artifact-name-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "must name the rendered unit", rule: ruleArtifact},
		{name: "egress-cidr-unspecified", file: "egress-cidr-unspecified.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "names every address", rule: ruleEgressCIDRReach},
		{name: "delivery-selector-key-malformed", file: "delivery-selector-key-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: selectorKeyRefusal, rule: ruleSelectorLabelKey},
		{name: "init-container-image-repository-malformed", file: "init-container-image-repository-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "is not a canonical image repository", rule: ruleImageRepository},
		{name: "init-container-named-twice", file: "init-container-named-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `init container "migrate" twice`, rule: ruleContainerUnique},
		{name: "egress-cidr-not-canonical-ipv6", file: "egress-cidr-not-canonical-ipv6.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "2001:db8::/32", rule: ruleEgressCIDRShape},
		{name: "egress-cidr-mapped-all-addresses", file: "egress-cidr-mapped-all-addresses.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: notCanonical, rule: ruleEgressCIDRShape},
		{name: "host-component-not-a-name", file: "host-component-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "component", rule: ruleHostName},
		{name: "host-domain-not-a-name", file: "host-domain-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "domain", rule: ruleHostName},
		{name: "host-coordinate-segment-not-a-name", file: "host-coordinate-segment-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "coordinate", rule: ruleHostName},
		{name: "trust-domain-with-a-space", file: "trust-domain-with-a-space.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "trust_domain", rule: ruleTrustDomain},
		{name: "service-over-qualified", file: "service-over-qualified.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "<module>/<service>", rule: ruleServiceQualified},
		{name: "egress-out-of-order", file: "egress-out-of-order.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "egress", rule: ruleEgressOrder},
		{name: "egress-with-empty-cidrs", file: "egress-with-empty-cidrs.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "neither a host nor a CIDR", rule: ruleEgressHasTarget},
		{name: "egress-cidrs-overlap-reversed", file: "egress-cidrs-overlap-reversed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: overlappingCIDRs, rule: ruleEgressCIDROver},
		{name: "egress-cidr-ipv6-all-addresses", file: "egress-cidr-ipv6-all-addresses.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "names every address", rule: ruleEgressCIDRReach},
		{name: "hostless-delivery-with-spiffe-id", file: "hostless-delivery-with-spiffe-id.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "but the cell declares no trust domain", rule: ruleSPIFFEIDHostless},
		{name: "selector-label-named-twice", file: "selector-label-named-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `names the key "app" twice`, rule: ruleMappingKeysOnce},
		{name: "selector-label-repeated", file: "selector-label-repeated.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "twice", rule: ruleMappingKeysOnce},
		// A fixture CAN catch a named added value, and a package assertion is
		// not run by a consumer's conformance call — so the widenings the
		// review named ship here, where cell.Run sees them.
		{name: "workload-kind-pod", file: "workload-kind-pod.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `kind "Pod" is not one of`, rule: ruleWorkloadKind},
		{name: "service-component-malformed", file: "service-component-malformed.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "<module>/<service>", rule: ruleServiceQualified},
		{name: "egress-host-not-a-hostname", file: "egress-host-not-a-hostname.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "is not a host name", rule: ruleEgressHostName},
		{name: "allow-modules-on-a-private-endpoint", file: "allow-modules-on-a-private-endpoint.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "an allow-list is only read for", rule: ruleAllowNeedsInner},
		{name: "null-key-hiding-a-subtree", file: "null-key-hiding-a-subtree.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: explicitNull, rule: ruleNoNulls},
		{name: "null-in-a-selector-value", file: "null-in-a-selector-value.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: explicitNull, rule: ruleNoNulls},
		// The two spellings the cutover DELETED: a consumer re-accepting them
		// reached every other fixture's outcome, and the package vocabulary
		// test is not run by a consumer's cell.Run.
		{name: "endpoint-visibility-module", file: "endpoint-visibility-module.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `visibility "module" is not one of`, rule: ruleVisibility},
		{name: "endpoint-visibility-external", file: "endpoint-visibility-external.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `visibility "external" is not one of`, rule: ruleVisibility},
		{name: "selector-key-empty", file: "selector-key-empty.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "a key that is not a name", rule: ruleKeyIsAName},
		// Kubernetes' own boundaries: each was caught by a package unit test
		// and by no shipped fixture, so a consumer loosening one passed the kit.
		{name: "selector-key-prefix-upper-case", file: "selector-key-prefix-upper-case.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: selectorKeyRefusal, rule: ruleSelectorLabelKey},
		{name: "selector-key-name-too-long", file: "selector-key-name-too-long.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: selectorKeyRefusal, rule: ruleSelectorLabelKey},
		{name: "selector-value-too-long", file: "selector-value-too-long.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "selector label", rule: ruleSelectorLabelVal},
		{name: "container-name-too-long", file: "container-name-too-long.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "is not a DNS label", rule: ruleContainerName},
		{name: "workload-name-too-long", file: "workload-name-too-long.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "is not a DNS subdomain", rule: ruleWorkloadName},
		{name: "egress-host-port-negative", file: "egress-host-port-negative.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "port -1", rule: ruleEgressHostPort},
		{name: "endpoint-port-fractional", file: "endpoint-port-fractional.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: notWhole, rule: ruleWholeNumbers},
		{name: "egress-host-port-fractional", file: "egress-host-port-fractional.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: notWhole, rule: ruleWholeNumbers},
		// A null in a LIST, which the null-key and null-value fixtures did not
		// reach: skipping only sequence-element nulls in the walker left a
		// null appended to cidrs, consumers and every other list silently
		// discarded, with the cell accepted.
		{name: "egress-cidr-null-list-entry", file: "egress-cidr-null-list-entry.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: explicitNull, rule: ruleNoNulls},
		// The SAME range twice. The overlap fixtures nest one range inside
		// another in both orders, so adding an inequality to the overlap test
		// admitted an identical pair.
		{name: "egress-cidrs-identical", file: "egress-cidrs-identical.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: overlappingCIDRs, rule: ruleEgressCIDROver},
		// An anchored FRACTION, declared as a key and used as a port: the
		// decoder follows the alias and truncates, so a check that examines
		// neither keys nor alias targets is one the document steps around.
		{name: "endpoint-port-aliased-fraction", file: "endpoint-port-aliased-fraction.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: notWhole, rule: ruleWholeNumbers},
		{name: "egress-host-port-aliased-fraction", file: "egress-host-port-aliased-fraction.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: notWhole, rule: ruleWholeNumbers},
		// The control: the same shape with a whole number is ACCEPTED, so the
		// two above refuse the fraction and not the alias.
		{name: "endpoint-port-aliased-integer", file: "endpoint-port-aliased-integer.yaml", outcome: OutcomeAccepted},
		// The two weakenings the confirming round named, each of which changed
		// NO fixture's outcome: the overlap witnesses were all IPv4, and the
		// non-canonical witness was the first entry.
		{name: "egress-cidrs-overlap-ipv6", file: "egress-cidrs-overlap-ipv6.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: overlappingCIDRs, rule: ruleEgressCIDROver},
		{name: "egress-cidr-not-canonical-later", file: "egress-cidr-not-canonical-later.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: notCanonical, rule: ruleEgressCIDRShape},
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
		{name: "image-without-digest", file: "image-without-digest.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: digestRefusal, rule: ruleImageDigest},
		{name: "image-digest-not-hex", file: "image-digest-not-hex.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "must pin an OCI manifest digest (sha256:<64 hex>)", rule: ruleImageDigest},
		{name: "delivery-image-without-digest", file: "delivery-image-without-digest.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `delivery container "deliver" must pin an OCI manifest digest`, rule: ruleImageDigest},
		{name: "authenticating-not-a-container", file: "authenticating-not-a-container.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "as its authenticating container, which is not one of its containers", rule: ruleAuthenticating},
		{name: "artifact-digest-not-hex", file: "artifact-digest-not-hex.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "the SHA-256 of its rendered bytes (sha256:<64 hex>)", rule: ruleArtifact},
		{name: "release-without-version", file: "release-without-version.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "release must carry publisher, name and version", rule: ruleReleaseWhole},
		{name: "endpoint-not-a-name", file: "endpoint-not-a-name.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `endpoint "GRPC" is not a lowercase name`, rule: ruleEndpointName},
		{name: "endpoint-declared-twice", file: "endpoint-declared-twice.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `endpoint "grpc" is declared twice`, rule: ruleEndpointUnique},
		{name: "endpoints-out-of-order", file: "endpoints-out-of-order.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "endpoints are not in name order", rule: ruleEndpointsOrder},
		{name: "endpoint-port-out-of-range", file: "endpoint-port-out-of-range.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "port 70000 is not a port", rule: ruleEndpointPort},
		{name: "endpoint-visibility-omitted", file: "endpoint-visibility-omitted.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "carries no visibility", rule: ruleVisibilityStated},
		{name: "endpoint-visibility-unknown", file: "endpoint-visibility-unknown.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `visibility "internal-only" is not one of`, rule: ruleVisibility},
		{name: "allow-modules-not-a-module", file: "allow-modules-not-a-module.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: `allow_modules names "Billing"`, rule: ruleAllowModules},
		{name: "allow-modules-without-internal", file: "allow-modules-without-internal.yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, message: "an allow-list is only read for", rule: ruleAllowNeedsInner},
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
