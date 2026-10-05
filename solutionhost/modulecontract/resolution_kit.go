package modulecontract

import (
	"errors"
	"strings"
)

// The resolution kit. The document kit proves a reader refuses the documents
// this package refuses; it says nothing about how a consumer's configuration
// reaches a slot, and that is the other half of this contract: two consumers
// resolving one composition into two different authority documents is the same
// failure as two readers disagreeing about a file.
//
// So a consumer is driven through these too, with the provider it actually
// renders from. A provider cannot decide anything — Values only enumerates —
// but it can still lose a record on the way here: collapse two spellings into
// one, drop the secret occurrence, deduplicate. Each fixture below is a
// configuration a provider must transfer faithfully, and the outcome core
// reaches from it.

// ResolutionFixture is one supplied configuration and the outcome every
// consumer's provider must produce for a slot resolving against it.
type ResolutionFixture struct {
	// Name is the fixture's stable name.
	Name string
	// Group is the workspace configuration group the slot points at.
	Group string
	// Key is the key the slot names, in the spelling the slot writes.
	Key string
	// Records are the group's records as the composition supplies them,
	// duplicates and both spellings included. A provider reports these.
	Records []Record
	// Outcome is accepted or refused.
	Outcome Outcome
	// Value is the value a resolved slot must carry when accepted.
	Value string
	// Sentinel is the error a refusal must match with errors.Is.
	Sentinel error
	// Message is a substring a refusal's message must carry.
	Message string
	// Rule names the ONE resolution rule this fixture isolates.
	Rule string
}

// The keys the resolution fixtures are written against, named once.
const (
	audienceKey    = "MODEL_AUDIENCE"
	audienceSlot   = "assistant/model-audience"
	audienceLookup = "model-audience"
	audienceValue  = "model-gateway"
)

// companion is the record the slot BESIDE the one under test resolves from, so
// every fixture is the group's records whole and a provider receives all of
// them.
var (
	companionKind     = Record{Key: "MODEL_RESOURCE_KIND", Value: "modelservice.profiles"}
	companionAudience = Record{Key: audienceKey, Value: audienceValue}
)

// withCompanion returns the fixture's records plus the companion, unless the
// fixture already supplies that key.
func withCompanion(records []Record, companion Record) []Record {
	for _, record := range records {
		if normalizeKey(record.Key) == normalizeKey(companion.Key) {
			return records
		}
	}
	return append(append([]Record(nil), records...), companion)
}

// ResolutionFixtures returns every shipped configuration with its outcome.
func ResolutionFixtures() []ResolutionFixture {
	const group, key = "assistant", audienceLookup
	fixtures := []ResolutionFixture{
		{
			Name: "one record", Group: group, Key: key, Outcome: OutcomeAccepted, Value: audienceValue,
			Records: []Record{{Key: audienceKey, Value: audienceValue}},
		},
		{
			// The convention core accepts: a slot written in one spelling
			// resolves a record written in the other.
			Name: "the other spelling", Group: group, Key: key, Outcome: OutcomeAccepted, Value: audienceValue,
			Records: []Record{{Key: audienceLookup, Value: audienceValue}},
		},
		{
			// Two spellings agreeing are one value: there is nothing a reader
			// could get wrong, so this must resolve rather than refuse.
			Name: "competing spellings that agree", Group: group, Key: key, Outcome: OutcomeAccepted, Value: audienceValue,
			Records: []Record{{Key: audienceKey, Value: audienceValue}, {Key: audienceLookup, Value: audienceValue}},
		},
		{
			Name: "the same record twice", Group: group, Key: key, Outcome: OutcomeAccepted, Value: audienceValue,
			Records: []Record{{Key: audienceKey, Value: audienceValue}, {Key: audienceKey, Value: audienceValue}},
		},
		{
			Name: "competing spellings that disagree", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: audienceKey + " and " + audienceLookup, Rule: ruleOneValue,
			Records: []Record{{Key: audienceKey, Value: audienceValue}, {Key: audienceLookup, Value: "other-gateway"}},
		},
		{
			Name: "the same spelling supplied twice with different values", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: "no one value to resolve", Rule: ruleOneValue,
			Records: []Record{{Key: audienceKey, Value: audienceValue}, {Key: audienceKey, Value: "other-gateway"}},
		},
		{
			// The disagreement the executed review found in a consumer's
			// adapter: it kept this public. A key any occurrence of which is
			// secret is a secret, and a slot resolves public configuration
			// only, so this refuses.
			Name: "a public and a secret occurrence", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrSecretSlot, Message: audienceSlot, Rule: ruleSecretPrecedence,
			Records: []Record{{Key: audienceKey, Value: "visible"}, {Key: audienceKey, Value: "hidden", Secret: true}},
		},
		{
			Name: "a secret occurrence in the other spelling", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrSecretSlot, Message: audienceSlot, Rule: ruleSecretPrecedence,
			Records: []Record{{Key: audienceLookup, Value: "hidden", Secret: true}},
		},
		{
			Name: "no record at all", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrUnresolvedSlot, Message: audienceSlot,
			Records: []Record{{Key: "EVIDENCE_AUDIENCE", Value: "documents"}},
		},
		{
			Name: "a value that is not one line", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: "not a single line", Rule: ruleResolvedName,
			Records: []Record{{Key: audienceKey, Value: "model gateway"}},
		},
		{
			Name: "an empty value", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: "empty or not a single line", Rule: ruleResolvedName,
			Records: []Record{{Key: audienceKey, Value: ""}},
		},
	}
	for index := range fixtures {
		fixtures[index].Records = withCompanion(fixtures[index].Records, companionKind)
	}
	return fixtures
}

// ResolvedKindFixtures are the same for the one slot whose value names a
// resource kind, which is concatenated into "<kind>:<action>": a comma or a
// colon in it carries scopes the contract never declared, so a resolved kind
// is held to the grammar a literal one is.
func ResolvedKindFixtures() []ResolutionFixture {
	const group, key = "assistant", "evidence-resource-kind"
	refused := func(name, value, message string) ResolutionFixture {
		return ResolutionFixture{
			Name: name, Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: message, Rule: ruleResolvedKind,
			Records: []Record{{Key: "EVIDENCE_RESOURCE_KIND", Value: value}},
		}
	}
	fixtures := []ResolutionFixture{
		{
			Name: "a resource kind", Group: group, Key: key, Outcome: OutcomeAccepted, Value: "documents.passages",
			Records: []Record{{Key: "EVIDENCE_RESOURCE_KIND", Value: "documents.passages"}},
		},
		refused("a kind carrying a comma and a colon", "documents.passages:delete,documents.passages", "not a lowercase resource kind"),
		refused("a kind carrying a colon", "documents.passages:delete", "not a lowercase resource kind"),
		refused("a kind carrying a comma", "documents.passages,documents.other", "not a lowercase resource kind"),
		refused("an upper-case kind", "Documents.Passages", "not a lowercase resource kind"),
	}
	for index := range fixtures {
		fixtures[index].Records = withCompanion(fixtures[index].Records, companionAudience)
	}
	return fixtures
}

// RunResolution drives a consumer's own value provider through every
// resolution fixture. The consumer passes the function that builds the
// provider it RENDERS from, out of a group's records the way its composition
// holds them — not a provider written for the test, which proves nothing about
// the renderer. A provider that collapses two spellings, drops the secret
// occurrence or deduplicates records fails here.
func RunResolution(t TestingT, provider func(group string, records []Record) Values) {
	t.Helper()
	if provider == nil {
		t.Fatalf("resolution conformance: no provider given")
		return
	}
	for _, fixture := range append(ResolutionFixtures(), ResolvedKindFixtures()...) {
		runResolutionFixture(t, fixture, provider(fixture.Group, fixture.Records), "")
	}
}

// runResolutionFixture resolves a one-binding contract whose slot is the
// fixture's, so what is exercised is Resolve itself rather than a helper.
func runResolutionFixture(t TestingT, fixture ResolutionFixture, values Values, without string) {
	t.Helper()
	kind := strings.HasSuffix(normalizeKey(fixture.Key), "_RESOURCE_KIND")
	binding := Binding{
		ID: "model", Operations: []string{OperationInvoke},
		ScopeCeiling: map[string]Ceiling{OperationInvoke: {Actions: []string{"read"}}},
		ResourceKind: &Slot{From: "assistant/model-resource-kind"},
	}
	if kind {
		// The kind slot is the one under test; the audience beside it
		// resolves from the fixture's companion record.
		binding.Audience = Slot{From: audienceSlot}
		binding.ResourceKind = &Slot{From: fixture.Group + "/" + fixture.Key}
	} else {
		binding.Audience = Slot{From: fixture.Group + "/" + fixture.Key}
	}
	contract := &Contract{
		Schema: SchemaV1, Principal: "assistant", Namespaces: []string{"assistant"}, Queues: []string{},
		Bindings: []Binding{binding},
	}
	if values == nil {
		values = recordValues{group: fixture.Group, records: fixture.Records}
	}
	resolved, err := contract.resolve(values, without)
	switch fixture.Outcome {
	case OutcomeAccepted:
		switch {
		case err != nil:
			t.Errorf("resolution fixture %q must resolve, got: %v", fixture.Name, err)
		case kind && resolved.Bindings[0].ResourceKind != fixture.Value:
			t.Errorf("resolution fixture %q must resolve to %q, got %q", fixture.Name, fixture.Value, resolved.Bindings[0].ResourceKind)
		case !kind && resolved.Bindings[0].Audience != fixture.Value:
			t.Errorf("resolution fixture %q must resolve to %q, got %q", fixture.Name, fixture.Value, resolved.Bindings[0].Audience)
		}
	case OutcomeRefused:
		switch {
		case err == nil:
			t.Errorf("resolution fixture %q must be refused with %v (the rule it isolates: %s)", fixture.Name, fixture.Sentinel, fixture.Rule)
		case !errors.Is(err, fixture.Sentinel):
			t.Errorf("resolution fixture %q must be refused with %v, got: %v", fixture.Name, fixture.Sentinel, err)
		case !strings.Contains(err.Error(), fixture.Message):
			t.Errorf("resolution fixture %q must be refused naming %q, got: %v", fixture.Name, fixture.Message, err)
		}
	}
}

// recordValues is the kit's own provider: it reports the records it was given,
// which is all a provider may do.
type recordValues struct {
	group   string
	records []Record
}

func (values recordValues) Records(group string) ([]Record, error) {
	if group != values.group {
		return nil, nil
	}
	return values.records, nil
}
