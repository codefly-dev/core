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
	// Audience, Kind, BindingKey and Scopes are the WHOLE resolved binding an
	// accepted case must produce — not only the field under test. A provider
	// that transferred the slot under test faithfully and corrupted the
	// companion record changed the derived authority and passed anyway.
	Audience   string
	Kind       string
	BindingKey string
	Scopes     []string
	// Sentinel is the error a refusal must match with errors.Is.
	Sentinel error
	// Message is a substring a refusal's message must carry.
	Message string
	// Rule names the ONE resolution rule this fixture isolates.
	Rule string
	// Slot is the binding slot under test — audience, resource_kind or
	// binding_key — stated rather than inferred from the key's spelling. It
	// was inferred, so every secret case landed on the audience and a
	// provider that declassified only the OTHER two roles passed all sixteen
	// cases while putting a secret into a scope.
	Slot string
}

// The keys the resolution fixtures are written against, named once.
const (
	audienceKey    = "MODEL_AUDIENCE"
	audienceSlot   = "assistant/model-audience"
	audienceLookup = "model-audience"
	audienceValue  = "model-gateway"
	kindValue      = "modelservice.profiles"
	kindScope      = kindValue + ":read"
	bindingKey     = "MODEL_BINDING"
	bindingValue   = "model"
	secretValue    = "hidden"
	passagesKind   = "documents.passages"
	notOneLine     = "empty or not a single line"
	noOneValue     = "no one value to resolve"
	bindingLookup  = "model-binding"
	kindKey        = "EVIDENCE_RESOURCE_KIND"
	kindSlot       = "assistant/evidence-resource-kind"
	bindingSlot    = "assistant/model-binding"
)

// companion is the record the slot BESIDE the one under test resolves from, so
// every fixture is the group's records whole and a provider receives all of
// them.
var (
	companionKind     = Record{Key: "MODEL_RESOURCE_KIND", Value: kindValue}
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
			Audience: audienceValue, Kind: kindValue, BindingKey: "", Scopes: []string{kindScope},
			Records: []Record{{Key: audienceKey, Value: audienceValue}},
		},
		{
			// The convention core accepts: a slot written in one spelling
			// resolves a record written in the other.
			Name: "the other spelling", Group: group, Key: key, Outcome: OutcomeAccepted, Value: audienceValue,
			Audience: audienceValue, Kind: kindValue, BindingKey: "", Scopes: []string{kindScope},
			Records: []Record{{Key: audienceLookup, Value: audienceValue}},
		},
		{
			// Two spellings agreeing are one value: there is nothing a reader
			// could get wrong, so this must resolve rather than refuse.
			Name: "competing spellings that agree", Group: group, Key: key, Outcome: OutcomeAccepted, Value: audienceValue,
			Audience: audienceValue, Kind: kindValue, BindingKey: "", Scopes: []string{kindScope},
			Records: []Record{{Key: audienceKey, Value: audienceValue}, {Key: audienceLookup, Value: audienceValue}},
		},
		{
			Name: "the same record twice", Group: group, Key: key, Outcome: OutcomeAccepted, Value: audienceValue,
			Audience: audienceValue, Kind: kindValue, BindingKey: "", Scopes: []string{kindScope},
			Records: []Record{{Key: audienceKey, Value: audienceValue}, {Key: audienceKey, Value: audienceValue}},
		},
		{
			Name: "competing spellings that disagree", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: audienceKey + " and " + audienceLookup, Rule: ruleOneValue,
			Records: []Record{{Key: audienceKey, Value: audienceValue}, {Key: audienceLookup, Value: "other-gateway"}},
		},
		{
			Name: "the same spelling supplied twice with different values", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: noOneValue, Rule: ruleOneValue,
			Records: []Record{{Key: audienceKey, Value: audienceValue}, {Key: audienceKey, Value: "other-gateway"}},
		},
		{
			// The disagreement the executed review found in a consumer's
			// adapter: it kept this public. A key any occurrence of which is
			// secret is a secret, and a slot resolves public configuration
			// only, so this refuses.
			Name: "a public and a secret occurrence", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrSecretSlot, Message: audienceSlot, Rule: ruleSecretPrecedence,
			Records: []Record{{Key: audienceKey, Value: "visible"}, {Key: audienceKey, Value: secretValue, Secret: true}},
		},
		{
			Name: "a secret occurrence in the other spelling", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrSecretSlot, Message: audienceSlot, Rule: ruleSecretPrecedence,
			Records: []Record{{Key: audienceLookup, Value: secretValue, Secret: true}},
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
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: audienceKey, Value: ""}},
		},
		{
			Name: "an audience carrying a next line", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: audienceKey, Value: "first\u0085second"}},
		},
		{
			Name: "an audience carrying a line separator", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: audienceKey, Value: "first\u2028second"}},
		},
		{
			// A NON-WHITESPACE control: every earlier case was also Unicode
			// whitespace, so removing the control test alone kept them all
			// refused.
			Name: "an audience carrying a NUL", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: audienceKey, Value: "first\x00second"}},
		},
		{
			Name: "an audience carrying a DEL", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: audienceKey, Value: "first\x7fsecond"}},
		},
		{
			// A FORMAT character, which is neither whitespace nor a control:
			// an invisible character in a name a receiver matches on.
			Name: "an audience carrying a zero-width space", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: audienceKey, Value: "first\u200bsecond"}},
		},
		{
			Name: "an audience carrying a zero-width joiner", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: audienceKey, Value: "first\u200dsecond"}},
		},
		{
			Name: "an audience carrying a word joiner", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: audienceKey, Value: "first\u2060second"}},
		},
		{
			Name: "an audience carrying a byte-order mark", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: audienceKey, Value: "first\ufeffsecond"}},
		},
	}
	for index := range fixtures {
		fixtures[index].Slot = fieldAudience
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
			Records: []Record{{Key: kindKey, Value: value}},
		}
	}
	fixtures := []ResolutionFixture{
		{
			Name: "a resource kind", Group: group, Key: key, Outcome: OutcomeAccepted, Value: passagesKind,
			Audience: audienceValue, Kind: passagesKind, BindingKey: "", Scopes: []string{"documents.passages:read"},
			Records: []Record{{Key: kindKey, Value: passagesKind}},
		},
		refused("a kind carrying a comma and a colon", "documents.passages:delete,documents.passages", "not a lowercase resource kind"),
		refused("a kind carrying a colon", "documents.passages:delete", "not a lowercase resource kind"),
		refused("a kind carrying a comma", "documents.passages,documents.other", "not a lowercase resource kind"),
		refused("an upper-case kind", "Documents.Passages", "not a lowercase resource kind"),
		{
			Name: "a kind carrying a line separator", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: kindKey, Value: "documents\u2028passages"}},
		},
		{
			// A provider that declassified only the resource-kind role put a
			// secret into a scope and passed every case, because every secret
			// case named the audience.
			Name: "a secret resource kind", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrSecretSlot, Message: kindSlot, Rule: ruleSecretPrecedence,
			Records: []Record{{Key: kindKey, Value: secretValue, Secret: true}},
		},
		{
			Name: "a resource kind supplied public and secret", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrSecretSlot, Message: kindSlot, Rule: ruleSecretPrecedence,
			Records: []Record{{Key: kindKey, Value: passagesKind}, {Key: kindKey, Value: secretValue, Secret: true}},
		},
		{
			Name: "no resource kind at all", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrUnresolvedSlot, Message: kindSlot,
			Records: []Record{{Key: "OTHER_RESOURCE_KIND", Value: passagesKind}},
		},
	}
	for index := range fixtures {
		fixtures[index].Slot = fieldResourceKind
		fixtures[index].Records = withCompanion(fixtures[index].Records, companionAudience)
	}
	return fixtures
}

// BindingKeyFixtures are the same for the slot whose value is the key the host
// installs the binding under: it had no resolution case at all, so a provider
// could lose it while every audience and kind case passed.
func BindingKeyFixtures() []ResolutionFixture {
	const group, key = "assistant", bindingLookup
	fixtures := []ResolutionFixture{
		{
			Name: "a binding key", Group: group, Key: key, Outcome: OutcomeAccepted, Value: bindingValue,
			Audience: audienceValue, Kind: kindValue, BindingKey: bindingValue,
			Scopes:  []string{kindScope},
			Records: []Record{{Key: bindingKey, Value: bindingValue}},
		},
		{
			Name: "a binding key supplied twice with different values", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: noOneValue, Rule: ruleOneValue,
			Records: []Record{{Key: bindingKey, Value: bindingValue}, {Key: "model-binding", Value: "other"}},
		},
		{
			Name: "a secret binding key", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrSecretSlot, Message: bindingSlot, Rule: ruleSecretPrecedence,
			Records: []Record{{Key: bindingKey, Value: secretValue, Secret: true}},
		},
		{
			Name: "a binding key supplied public and secret", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrSecretSlot, Message: bindingSlot, Rule: ruleSecretPrecedence,
			Records: []Record{{Key: bindingKey, Value: bindingValue}, {Key: "model-binding", Value: secretValue, Secret: true}},
		},
		{
			Name: "a binding key carrying a paragraph separator", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: bindingKey, Value: "model\u2029other"}},
		},
		{
			Name: "a binding key carrying a newline", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: bindingKey, Value: "model\nother"}},
		},
		{
			Name: "a binding key carrying a DEL", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: bindingKey, Value: "model\x7fother"}},
		},
		{
			Name: "a binding key carrying a zero-width space", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrAmbiguousSlot, Message: notOneLine, Rule: ruleResolvedName,
			Records: []Record{{Key: bindingKey, Value: "model\u200bother"}},
		},
		{
			Name: "no binding key at all", Group: group, Key: key, Outcome: OutcomeRefused,
			Sentinel: ErrUnresolvedSlot, Message: bindingSlot,
			Records: []Record{{Key: "OTHER_BINDING", Value: bindingValue}},
		},
	}
	for index := range fixtures {
		fixtures[index].Slot = fieldBindingKey
		fixtures[index].Records = withCompanion(withCompanion(fixtures[index].Records, companionKind), companionAudience)
	}
	return fixtures
}

// lossFixtures are the cases that catch a provider DISCARDING a record rather
// than mis-reading one, generated for EVERY slot role so none can be missed,
// and generated over EVERY axis so no combination can be missed either:
// a conflict in one spelling and in two, then a public/secret collision over
// all four combinations of order (public first, secret first) and spelling
// (one, two) — in both the agreeing and the disagreeing value.
//
// Each axis was a false certification, not a hypothetical:
//
//   - The ROLE axis: two last-wins adapters passed every case, because the
//     resource-kind role had no conflicting pair at all and the binding-key
//     conflict used two spellings, which an adapter indexing by exact spelling
//     never collapses.
//   - The VALUE-AGREEMENT axis: every collision disagreed on the value, so an
//     adapter deduplicating by (Key, Value) was caught by the one-value rule;
//     with the value AGREEING only the flag carries the refusal, and it
//     resolved.
//   - The SPELLING-ORDER axis: the public-then-secret collision existed in one
//     spelling only, so an adapter dropping a secret that arrives under a
//     DIFFERENT spelling than an earlier occurrence found no case to drop for
//     the audience and resource-kind roles — the kit never supplied the shape.
//
// Writing the combinations out by hand is what produced each gap, so they are
// generated.
func lossFixtures(role, group, key, upper, lower, value string) []ResolutionFixture {
	const other = "other.value"
	slot := group + "/" + key
	fixtures := []ResolutionFixture{
		{
			Name: role + ": one spelling supplied twice with different values", Group: group, Key: key,
			Outcome: OutcomeRefused, Sentinel: ErrAmbiguousSlot, Message: noOneValue, Rule: ruleOneValue,
			Slot:    role,
			Records: []Record{{Key: upper, Value: value}, {Key: upper, Value: other}},
		},
		{
			Name: role + ": two spellings supplied with different values", Group: group, Key: key,
			Outcome: OutcomeRefused, Sentinel: ErrAmbiguousSlot, Message: noOneValue, Rule: ruleOneValue,
			Slot:    role,
			Records: []Record{{Key: upper, Value: value}, {Key: lower, Value: other}},
		},
	}
	for _, agreeing := range []bool{true, false} {
		// The secret's value: the same as the public one, or a different one.
		// Agreeing is when ONLY the Secret flag carries the refusal;
		// disagreeing is when the one-value rule would also catch a provider
		// that kept both records.
		secret := secretValue
		agreement := "a different value"
		if agreeing {
			secret = value
			agreement = "the same value"
		}
		for _, secretFirst := range []bool{false, true} {
			for _, twoSpellings := range []bool{false, true} {
				publicKey, secretKey := upper, upper
				spelling := "one spelling"
				if twoSpellings {
					secretKey = lower
					spelling = "two spellings"
				}
				order := "public then secret"
				records := []Record{{Key: publicKey, Value: value}, {Key: secretKey, Value: secret, Secret: true}}
				if secretFirst {
					order = "secret then public"
					records = []Record{{Key: secretKey, Value: secret, Secret: true}, {Key: publicKey, Value: value}}
				}
				fixtures = append(fixtures, ResolutionFixture{
					Name:  role + ": " + order + ", " + agreement + ", " + spelling,
					Group: group, Key: key,
					Outcome: OutcomeRefused, Sentinel: ErrSecretSlot, Message: slot, Rule: ruleSecretPrecedence,
					Slot:    role,
					Records: records,
				})
			}
		}
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
	for _, fixture := range AllResolutionFixtures() {
		values := provider(fixture.Group, fixture.Records)
		if values == nil {
			// Refused at the boundary: a factory that builds no provider —
			// an adapter whose construction failed — would otherwise be
			// substituted by the reference provider below and receive a
			// conformance pass for testing core against itself.
			t.Fatalf("resolution conformance: the provider factory returned no Values for fixture %q; a kit cannot certify an adapter that was not built", fixture.Name)
			return
		}
		runResolutionFixture(t, fixture, values, "")
	}
}

// runResolutionFixture resolves a one-binding contract whose slot is the
// fixture's, so what is exercised is Resolve itself rather than a helper.
func runResolutionFixture(t TestingT, fixture ResolutionFixture, values Values, without string) {
	t.Helper()
	if values == nil {
		t.Fatalf("resolution fixture %q was run with no provider", fixture.Name)
		return
	}
	role := fixture.Slot
	if role == "" {
		role = fieldAudience
	}
	binding := Binding{
		ID: "model", Operations: []string{OperationInvoke},
		ScopeCeiling: map[string]Ceiling{OperationInvoke: {Actions: []string{"read"}}},
		ResourceKind: &Slot{From: "assistant/model-resource-kind"},
	}
	switch role {
	case fieldResourceKind:
		// The kind slot is the one under test; the audience beside it
		// resolves from the fixture's companion record.
		binding.Audience = Slot{From: audienceSlot}
		binding.ResourceKind = &Slot{From: fixture.Group + "/" + fixture.Key}
	case fieldBindingKey:
		binding.Audience = Slot{From: audienceSlot}
		binding.BindingKey = &Slot{From: fixture.Group + "/" + fixture.Key}
	default:
		binding.Audience = Slot{From: fixture.Group + "/" + fixture.Key}
	}
	contract := &Contract{
		Schema: SchemaV1, Principal: "assistant", Namespaces: []string{"assistant"}, Queues: []string{},
		Bindings: []Binding{binding},
	}
	resolved, err := contract.resolve(values, without)
	switch fixture.Outcome {
	case OutcomeAccepted:
		if err != nil {
			t.Errorf("resolution fixture %q must resolve, got: %v", fixture.Name, err)
			return
		}
		// The WHOLE binding, so a provider cannot corrupt a value the fixture
		// does not name and still pass.
		got := resolved.Bindings[0]
		for _, field := range []struct {
			what, have, want string
		}{
			{"audience", got.Audience, fixture.Audience},
			{"resource_kind", got.ResourceKind, fixture.Kind},
			{"binding_key", got.BindingKey, fixture.BindingKey},
		} {
			if field.have != field.want {
				t.Errorf("resolution fixture %q must resolve %s to %q, got %q", fixture.Name, field.what, field.want, field.have)
			}
		}
		if have := strings.Join(got.Scopes[OperationInvoke], ","); have != strings.Join(fixture.Scopes, ",") {
			t.Errorf("resolution fixture %q must produce scopes %v, got %v", fixture.Name, fixture.Scopes, got.Scopes[OperationInvoke])
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

// AllResolutionFixtures is every shipped resolution case.
func AllResolutionFixtures() []ResolutionFixture {
	all := append([]ResolutionFixture(nil), ResolutionFixtures()...)
	all = append(all, ResolvedKindFixtures()...)
	all = append(all, BindingKeyFixtures()...)
	for _, role := range []struct{ role, key, upper, lower, value string }{
		{fieldAudience, audienceLookup, audienceKey, audienceLookup, audienceValue},
		{fieldResourceKind, "evidence-resource-kind", kindKey, "evidence-resource-kind", passagesKind},
		{fieldBindingKey, bindingLookup, bindingKey, bindingLookup, bindingValue},
	} {
		for _, fixture := range lossFixtures(role.role, "assistant", role.key, role.upper, role.lower, role.value) {
			fixture.Records = withCompanion(withCompanion(fixture.Records, companionKind), companionAudience)
			all = append(all, fixture)
		}
	}
	return all
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
