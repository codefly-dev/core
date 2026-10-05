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
	all := append(ResolutionFixtures(), ResolvedKindFixtures()...)
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
			runResolutionFixture(recorder, fixture, nil, rule)
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
		"keeps only the first spelling": func(records []Record) []Record {
			if len(records) == 0 {
				return records
			}
			return records[:1]
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
		"normalizes the keys itself": func(records []Record) []Record {
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
