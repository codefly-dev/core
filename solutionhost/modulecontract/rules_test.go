package modulecontract

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
		// The kit, run against this reader with the rule deleted, must fail.
		recorder := &recordingT{}
		Run(recorder, func(document []byte) error {
			_, err := parse(document, r.name)
			return err
		})
		if recorder.failures == 0 {
			t.Errorf("rule %s can be deleted and the kit still passes: %d fixture(s) name it but none notices", r.name, len(fixtures))
		}
		// And each fixture naming the rule is no longer refused BY it.
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
		"action-declared-twice", "action-not-a-name", "another-schema",
		"audience-from-profile-key", "bare-action-tagged",
		"bare-actions-without-resource-kind", "binding-action-declared-twice",
		"binding-action-not-a-name", "binding-id-not-a-name",
		"binding-key-from-bare-name", "binding-without-operation",
		"ceiling-entry-actions-not-a-list", "ceiling-entry-alias-key",
		"ceiling-entry-names-a-field-twice", "ceiling-entry-with-empty-field-name",
		"ceiling-entry-with-own-field", "ceiling-for-undeclared-operation",
		"ceiling-not-a-list", "destination-declared-twice",
		"destination-endpoint-not-a-name", "destination-id-not-a-name",
		"destination-service-not-a-name", "duplicate-binding", "kind-named-twice",
		"kind-with-no-action", "literal-audience", "lookup-method-not-a-name",
		"lookup-method-without-lookup", "mixed-ceiling",
		"namespace-declared-twice", "namespace-not-a-name", "namespaces-omitted",
		"not-yaml", "operation-declared-twice", "operation-without-ceiling",
		"principal-upper-case", "queue-declared-twice", "queue-not-a-name",
		"queues-omitted", "resource-kind-from-endpoint-key", "schema-omitted",
		"scope-ceiling-kind-declared-twice", "scope-ceiling-kind-not-a-name",
		"scope-ceiling-without-action", "scope-kind-not-a-name", "slot-alias-key",
		"slot-names-from-twice", "slot-reference-tagged", "slot-with-default",
		"slot-with-empty-field-name", "slot-without-group", "tenancy",
		"two-documents", "unknown-destination-kind", "unknown-operation",
		"upper-case-slot-key", "valid",
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
	if recorder.failures == 0 || !strings.Contains(recorder.messages, "module contract fixture tenancy must be refused") {
		t.Fatalf("the kit did not name the rule the reader skipped:\n%s", recorder.messages)
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
