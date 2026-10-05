package cell

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestEveryRuleIsProtectedByAFixture is the kit's self-check: every rule the
// reader enforces is named by at least one refused fixture, every refused
// fixture names a rule that exists, and deleting any one rule fails the kit
// — on a fixture naming that rule, which the deletion lets through or hands
// to a later rule with another message. A rule this test cannot falsify is a
// rule the kit does not protect.
func TestEveryRuleIsProtectedByAFixture(t *testing.T) {
	all, err := Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	names := ruleNames()
	protected := map[string][]Fixture{}
	for _, fixture := range all {
		switch fixture.Outcome {
		case OutcomeAccepted:
			if fixture.Rule != "" {
				t.Errorf("accepted fixture %s names rule %s", fixture.Name, fixture.Rule)
			}
		case OutcomeRefused:
			if !slices.Contains(names, fixture.Rule) {
				t.Errorf("refused fixture %s names rule %q, which does not exist", fixture.Name, fixture.Rule)
			}
			protected[fixture.Rule] = append(protected[fixture.Rule], fixture)
		}
	}
	for _, r := range rules() {
		fixtures := protected[r.name]
		if len(fixtures) == 0 {
			t.Errorf("rule %s is protected by no fixture: a reader could drop it and pass the kit", r.name)
			continue
		}
		if r.inherent {
			continue
		}
		recorder := &recordingT{}
		Run(recorder, func(document []byte) error {
			_, err := parse(document, r.name)
			return err
		})
		if recorder.failures == 0 {
			t.Errorf("rule %s can be deleted and the kit still passes: %d fixture(s) name it but none notices", r.name, len(fixtures))
		}
		for _, fixture := range fixtures {
			_, err := parse(fixture.Document, r.name)
			if err != nil && strings.Contains(err.Error(), fixture.Message) {
				t.Errorf("fixture %s is still refused naming %q with rule %s deleted: the refusal comes from somewhere else", fixture.Name, fixture.Message, r.name)
			}
		}
	}
}

// TestEveryFixtureReachesItsOutcome runs the kit against this package's own
// reader, and holds the kit to its exact contents: a fixture that drifted
// from the reader, or one that was dropped, is caught here first.
func TestEveryFixtureReachesItsOutcome(t *testing.T) {
	Run(t, func(document []byte) error {
		_, err := Parse(document)
		return err
	})
	all, err := Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(all))
	for _, fixture := range all {
		names = append(names, fixture.Name)
	}
	sort.Strings(names)
	want := []string{
		"account-not-a-subdomain", "allow-modules-not-a-module",
		"allow-modules-without-internal", "another-schema",
		"artifact-digest-not-hex", "artifact-digest-too-short",
		"artifact-name-not-a-name", "authenticating-is-an-init-container",
		"authenticating-not-a-container", "binding-declared-twice",
		"binding-not-a-name", "consumer-named-twice", "consumer-not-qualified",
		"consumers-out-of-order", "container-name-not-a-label",
		"container-named-twice", "delivery-account-malformed",
		"delivery-container-not-a-label", "delivery-image-repository-malformed",
		"delivery-image-without-digest", "delivery-not-a-job",
		"delivery-selector-label-malformed",
		"delivery-spiffe-id-of-another-account", "delivery-without-selector",
		"egress-cidr-malformed", "egress-cidr-not-canonical",
		"egress-cidr-unspecified", "egress-cidrs-overlap", "egress-host-malformed",
		"egress-host-port-out-of-range", "egress-host-without-port",
		"egress-of-another-module", "egress-service-not-qualified",
		"egress-service-twice", "egress-without-target", "empty-selector",
		"endpoint-declared-twice", "endpoint-not-a-name",
		"endpoint-port-out-of-range", "endpoint-visibility-omitted",
		"endpoint-visibility-unknown", "endpoints-out-of-order",
		"environment-not-a-name", "host-coordinate-not-a-name", "hostless",
		"image-digest-not-hex", "image-digest-too-short",
		"image-digest-without-prefix", "image-repository-carries-digest",
		"image-repository-carries-tag", "image-repository-padded",
		"image-without-digest", "ingress-host-malformed",
		"ingress-host-named-twice", "ingress-out-of-order",
		"ingress-to-an-endpoint-not-served", "ingress-to-one-endpoint-twice",
		"ingress-without-host", "init-container-image-without-digest",
		"init-container-name-not-a-label", "module-in-two-namespaces",
		"namespace-declared-twice", "namespace-module-not-a-name",
		"namespace-not-a-label", "namespaces-out-of-order", "not-yaml",
		"partial-host-header", "release-without-name", "release-without-publisher",
		"release-without-version", "schema-omitted", "selector-empty-label-value",
		"selector-label-key-malformed", "selector-label-value-malformed",
		"service-not-qualified", "service-of-another-module",
		"spiffe-id-of-another-account", "spiffe-id-of-another-namespace",
		"spiffe-id-of-another-trust-domain", "spiffe-id-without-trust-domain",
		"trust-domain-malformed", "two-documents", "unknown-field",
		"unknown-workload-kind", "valid", "workload-declared-twice",
		"workload-name-not-a-subdomain", "workload-without-container",
		"workloads-out-of-order",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("the kit ships %v; a changed kit is a changed rule set, declared here", names)
	}
}

// TestTheKitFailsAReaderThatSkipsARule: a reader that decodes but does not
// validate passes the accepted fixtures and fails the refusals by name, which
// is what makes the kit a gate rather than a round-trip; a reader that
// validates loosely (ignoring unknown fields) fails it too.
func TestTheKitFailsAReaderThatSkipsARule(t *testing.T) {
	recorder := &recordingT{}
	Run(recorder, func(document []byte) error {
		_, err := parse(document, ruleKnownFields)
		return err
	})
	if recorder.failures == 0 || !strings.Contains(recorder.messages, "cell fixture unknown-field must be refused") {
		t.Fatalf("the kit did not name the rule the reader skipped:\n%s", recorder.messages)
	}
}

// TestLabelsAreHeldToKubernetesGrammar pins the grammars to Kubernetes' own
// boundaries, on both sides of each.
func TestLabelsAreHeldToKubernetesGrammar(t *testing.T) {
	for value, want := range map[string]bool{
		"app": true, "app.kubernetes.io/name": true, "A-b_c.9": true, "a": true, "": false, "-a": false, "a-": false,
		"bad/key/x": false, "/name": false, "prefix/": false, "UPPER.example/Name": false, "example.com/" + strings.Repeat("a", 63): true,
		"example.com/" + strings.Repeat("a", 64): false, strings.Repeat("a", 64): false,
	} {
		if got := isQualifiedName(value); got != want {
			t.Errorf("label key %q: got %v, want %v", value, got, want)
		}
	}
	for value, want := range map[string]bool{
		"": true, "api": true, "A_b-c.9": true, "-a": false, "a-": false, "bad/value": false, "a b": false,
		strings.Repeat("a", 63): true, strings.Repeat("a", 64): false,
	} {
		if got := isLabelValue(value); got != want {
			t.Errorf("label value %q: got %v, want %v", value, got, want)
		}
	}
	for value, want := range map[string]bool{
		"payments": true, "pay-ments": true, "payments.v2": false, "Payments": false, "": false, strings.Repeat("a", 63): true, strings.Repeat("a", 64): false,
	} {
		if got := isDNS1123Label(value); got != want {
			t.Errorf("DNS label %q: got %v, want %v", value, got, want)
		}
	}
	for value, want := range map[string]bool{
		"api": true, "api.v2": true, "API": false, "api_sa": false, "a..b": false, strings.Repeat("a", 253): true, strings.Repeat("a", 254): false,
	} {
		if got := isDNS1123Subdomain(value); got != want {
			t.Errorf("DNS subdomain %q: got %v, want %v", value, got, want)
		}
	}
}

// TestRepositoriesAreCanonical pins what a repository may be: a registry and
// a path, ports kept, nothing a digest beside it would duplicate.
func TestRepositoriesAreCanonical(t *testing.T) {
	for repository, want := range map[string]bool{
		"ghcr.io/example/payments-api": true, "docker.io/curlimages/curl": true, "localhost:5000/payments": true,
		"registry.example.test:5000/team/api": true, "k3d-registry:5000/api": true,
		" ": false, "": false, "ghcr.io/example/api:1.2": false, "ghcr.io/example/api@sha256:" + strings.Repeat("a", 64): false,
		"ghcr.io/Example/Api": false, "curl": false, "example/api": false, "ghcr.io/example/api ": false,
	} {
		if got := repositoryDefect(repository) == ""; got != want {
			t.Errorf("repository %q: accepted %v, want %v (%s)", repository, got, want, repositoryDefect(repository))
		}
	}
}

type recordingT struct {
	failures int
	messages string
}

func (r *recordingT) Helper() {}

func (r *recordingT) Errorf(format string, args ...any) {
	r.failures++
	r.messages += strings.TrimSpace(fmt.Sprintf(format, args...)) + "\n"
}

func (r *recordingT) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
}
