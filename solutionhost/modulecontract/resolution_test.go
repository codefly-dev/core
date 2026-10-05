package modulecontract

import (
	"strings"
	"testing"
)

// TestEveryResolutionFixtureReachesItsOutcome runs the resolution kit against
// this package's own resolution: the reference implementation reaches its own
// outcomes, and a fixture whose outcome drifted is caught here first.
func TestEveryResolutionFixtureReachesItsOutcome(t *testing.T) {
	RunResolution(t, func(group string, records []Record) Values {
		return recordValues{group: group, records: records}
	})
}

// TestEveryResolutionRuleIsProtectedByAFixture is the resolution half's
// completeness, and the answer to "the kit cannot detect disagreement there":
// every resolution rule is isolated by a fixture, and each rule is removed in
// turn — the records still read, the refusal not raised — and the fixture that
// isolates it must notice. A resolution rule that could be dropped silently is
// a rule a consumer could resolve differently without failing the kit.
func TestEveryResolutionRuleIsProtectedByAFixture(t *testing.T) {
	all := AllResolutionFixtures()
	protected := map[string][]ResolutionFixture{}
	for _, fixture := range all {
		if fixture.Outcome == OutcomeRefused && fixture.Rule != "" {
			protected[fixture.Rule] = append(protected[fixture.Rule], fixture)
		}
	}
	for _, rule := range resolutionRuleNames() {
		fixtures := protected[rule]
		if len(fixtures) == 0 {
			t.Errorf("resolution rule %s is protected by no fixture: a provider could resolve past it and pass the kit", rule)
			continue
		}
		recorder := &recordingT{}
		for _, fixture := range all {
			runResolutionFixture(recorder, fixture, recordValues{group: fixture.Group, records: fixture.Records}, rule)
		}
		if recorder.failures == 0 {
			t.Errorf("resolution rule %s can be deleted and the kit still passes: %d fixture(s) name it but none notices", rule, len(fixtures))
		}
		for _, fixture := range fixtures {
			if !strings.Contains(recorder.messages, "fixture "+quote(fixture.Name)+" must be refused") {
				t.Errorf("deleting resolution rule %s did not change the outcome of %q, the fixture that isolates it:\n%s", rule, fixture.Name, recorder.messages)
			}
		}
	}
}

func quote(value string) string { return `"` + value + `"` }

// TestAProviderThatLosesARecordFailsTheKit: the shape of Values forecloses a
// provider DECIDING anything, but not a provider dropping a record on the way
// here — collapsing two spellings, discarding the secret occurrence,
// deduplicating. Each of those is a consumer that resolves a composition
// differently from core, and each fails the kit by name.
func TestAProviderThatLosesARecordFailsTheKit(t *testing.T) {
	for name, lose := range map[string]func([]Record) []Record{
		// Keeps the FIRST record of each normalized key and discards the
		// rest, which is what an adapter indexing by key does. It must be
		// caught by the conflicting-record fixtures, not by an unrelated
		// slot going missing, so the companion record is left in place.
		"keeps only the first occurrence of a key": func(records []Record) []Record {
			seen := map[string]bool{}
			kept := make([]Record, 0, len(records))
			for _, record := range records {
				if seen[normalizeKey(record.Key)] {
					continue
				}
				seen[normalizeKey(record.Key)] = true
				kept = append(kept, record)
			}
			return kept
		},
		"drops the secret occurrences": func(records []Record) []Record {
			kept := make([]Record, 0, len(records))
			for _, record := range records {
				if !record.Secret {
					kept = append(kept, record)
				}
			}
			return kept
		},
		// Deduplicates by exact spelling, so a key supplied twice under one
		// spelling collapses while two spellings both survive.
		"deduplicates each exact spelling": func(records []Record) []Record {
			seen := map[string]bool{}
			kept := make([]Record, 0, len(records))
			for _, record := range records {
				if seen[record.Key] {
					continue
				}
				seen[record.Key] = true
				kept = append(kept, record)
			}
			return kept
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := &recordingT{}
			RunResolution(recorder, func(group string, records []Record) Values {
				return recordValues{group: group, records: lose(records)}
			})
			if recorder.failures == 0 {
				t.Fatalf("a provider that %s passed the resolution kit", name)
			}
		})
	}
}

// TestAFactoryThatBuildsNoProviderFailsTheKit: a factory whose adapter
// construction failed used to receive a conformance pass, because its nil
// result was replaced by the reference provider and the kit then tested core
// against itself.
func TestAFactoryThatBuildsNoProviderFailsTheKit(t *testing.T) {
	recorder := &recordingT{}
	RunResolution(recorder, func(string, []Record) Values { return nil })
	if recorder.failures == 0 {
		t.Fatal("a factory that builds no provider passed the resolution kit")
	}
	if !strings.Contains(recorder.messages, "cannot certify an adapter that was not built") {
		t.Fatalf("the kit did not say why:\n%s", recorder.messages)
	}
}

// TestAProviderThatCorruptsACompanionValueFailsTheKit is finding 2's
// regression: a provider that transfers the record under test faithfully and
// replaces the COMPANION resource kind changes the derived authority — the
// accepted audience cases then produce "other.profiles:read" — and used to
// pass, because each case checked only the field it was named for.
func TestAProviderThatCorruptsACompanionValueFailsTheKit(t *testing.T) {
	recorder := &recordingT{}
	RunResolution(recorder, func(group string, records []Record) Values {
		corrupted := make([]Record, 0, len(records))
		for _, record := range records {
			if normalizeKey(record.Key) == "MODEL_RESOURCE_KIND" {
				record.Value = "other.profiles"
			}
			corrupted = append(corrupted, record)
		}
		return recordValues{group: group, records: corrupted}
	})
	if recorder.failures == 0 {
		t.Fatal("a provider that corrupts a companion value passed the resolution kit")
	}
	if !strings.Contains(recorder.messages, "other.profiles") {
		t.Fatalf("the kit did not name the corrupted value:\n%s", recorder.messages)
	}
}

// TestAProviderThatDeclassifiesANonAudienceSlotFailsTheKit is the executed
// round's finding: every secret case named the AUDIENCE, so a provider that
// cleared Secret only for keys ending _RESOURCE_KIND or _BINDING passed all
// sixteen cases — and put a secret value into a resolved resource kind, and so
// into a scope string. The slot under test is stated in the fixture now, and
// every role carries its own secret, collision and missing cases.
func TestAProviderThatDeclassifiesANonAudienceSlotFailsTheKit(t *testing.T) {
	for _, suffix := range []string{"_RESOURCE_KIND", "_BINDING", "_AUDIENCE"} {
		t.Run(suffix, func(t *testing.T) {
			recorder := &recordingT{}
			RunResolution(recorder, func(group string, records []Record) Values {
				declassified := make([]Record, 0, len(records))
				for _, record := range records {
					if strings.HasSuffix(normalizeKey(record.Key), suffix) {
						record.Secret = false
					}
					declassified = append(declassified, record)
				}
				return recordValues{group: group, records: declassified}
			})
			if recorder.failures == 0 {
				t.Fatalf("a provider that declassifies every %s key passed the resolution kit", suffix)
			}
		})
	}
}
