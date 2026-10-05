package modulecontract

import (
	"fmt"
	"os"
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
		// A provider faithful in EVERYTHING except the Secret flag on a lone
		// record whose key is spelled with the _PREFIX convention. Every
		// collision case supplies two records for the slot's key, so this
		// changed none of the shipped configurations and was certified --
		// and Resolve then succeeded with the secret's own value as the
		// audience, where faithful transfer gives ErrSecretSlot. The two
		// singleton cases per convention exist for this.
		"declassifies a lone _PREFIX secret": func(records []Record) []Record {
			counts := map[string]int{}
			for _, record := range records {
				counts[normalizeKey(record.Key)]++
			}
			kept := make([]Record, 0, len(records))
			for _, record := range records {
				normalized := normalizeKey(record.Key)
				if strings.HasSuffix(normalized, "_PREFIX") && counts[normalized] == 1 {
					record.Secret = false
				}
				kept = append(kept, record)
			}
			return kept
		},
		// The same loss without the _PREFIX spelling: a lone secret record
		// for any slot, declassified.
		"declassifies a lone secret record": func(records []Record) []Record {
			counts := map[string]int{}
			for _, record := range records {
				counts[normalizeKey(record.Key)]++
			}
			kept := make([]Record, 0, len(records))
			for _, record := range records {
				if counts[normalizeKey(record.Key)] == 1 {
					record.Secret = false
				}
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

// TestEveryRoleIsProtectedAgainstRecordLoss drives a faulty provider at ONE
// slot role at a time, over every axis an adapter can be wrong on: which
// spelling it indexes by, whether it keeps the first or the last record, and
// whether it drops a secret arriving under a different spelling. A regression
// that applies loss to EVERY role and asks only whether SOME fixture failed
// stays green after one role's protection disappears entirely — which is how
// deleting all five of a role's generated cases survived the whole suite. So
// each case here must fail, AND the fixture that fails must belong to the role
// under test.
func TestEveryRoleIsProtectedAgainstRecordLoss(t *testing.T) {
	for _, convention := range lossRoles() {
		for _, lossy := range lossyProviders() {
			t.Run(convention.label+"/"+lossy.name, func(t *testing.T) {
				recorder := &recordingT{}
				RunResolution(recorder, func(group string, records []Record) Values {
					return recordValues{group: group, records: lossy.apply(records, convention)}
				})
				if recorder.failures == 0 {
					t.Fatalf("a provider that %s for the %s convention passed the resolution kit", lossy.name, convention.label)
				}
				if !recorder.failedNaming(convention.label) {
					t.Fatalf("a provider that %s for the %s convention was caught, but by no fixture of it: %v",
						lossy.name, convention.label, recorder.messages)
				}
			})
		}
	}
}

type lossyProvider struct {
	name  string
	apply func(records []Record, convention lossRole) []Record
}

// lossyProviders are the record-losing adapters each role must be protected
// against. Every one of them received a green kit verdict at some head.
func lossyProviders() []lossyProvider {
	keep := func(keyOf func(Record) string, last bool) func([]Record, lossRole) []Record {
		return func(records []Record, forRole lossRole) []Record {
			chosen := map[string]Record{}
			var order []string
			for _, record := range records {
				if !belongsTo(record, forRole) {
					continue
				}
				key := keyOf(record)
				if _, seen := chosen[key]; !seen {
					order = append(order, key)
				} else if !last {
					continue
				}
				chosen[key] = record
			}
			kept := make([]Record, 0, len(records))
			for _, record := range records {
				if !belongsTo(record, forRole) {
					kept = append(kept, record)
				}
			}
			for _, key := range order {
				kept = append(kept, chosen[key])
			}
			return kept
		}
	}
	normalized := func(record Record) string { return normalizeKey(record.Key) }
	exact := func(record Record) string { return record.Key }
	return []lossyProvider{
		{"keeps only the last record per normalized key", keep(normalized, true)},
		{"keeps only the last record per exact spelling", keep(exact, true)},
		{"keeps only the first record per normalized key", keep(normalized, false)},
		{"keeps only the first record per exact spelling", keep(exact, false)},
		{"deduplicates by (key, value), ignoring Secret", func(records []Record, role lossRole) []Record {
			type pair struct{ key, value string }
			seen := map[pair]bool{}
			kept := make([]Record, 0, len(records))
			for _, record := range records {
				at := pair{record.Key, record.Value}
				if belongsTo(record, role) && seen[at] {
					continue
				}
				seen[at] = true
				kept = append(kept, record)
			}
			return kept
		}},
		{"drops a later UPPER secret after a lower public record", func(records []Record, role lossRole) []Record {
			first := map[string]string{}
			kept := make([]Record, 0, len(records))
			for _, record := range records {
				norm := normalizeKey(record.Key)
				prior, seen := first[norm]
				lowerFirst := seen && prior != norm
				if belongsTo(record, role) && record.Secret && lowerFirst && record.Key == norm {
					continue
				}
				if !seen {
					first[norm] = record.Key
				}
				kept = append(kept, record)
			}
			return kept
		}},
		{"drops a later lower secret after an UPPER public record", func(records []Record, role lossRole) []Record {
			first := map[string]string{}
			kept := make([]Record, 0, len(records))
			for _, record := range records {
				norm := normalizeKey(record.Key)
				prior, seen := first[norm]
				if belongsTo(record, role) && record.Secret && seen && prior == norm && record.Key != norm {
					continue
				}
				if !seen {
					first[norm] = record.Key
				}
				kept = append(kept, record)
			}
			return kept
		}},
		{"drops a secret arriving under another spelling", func(records []Record, role lossRole) []Record {
			first := map[string]string{}
			kept := make([]Record, 0, len(records))
			for _, record := range records {
				norm := normalizeKey(record.Key)
				prior, seen := first[norm]
				if belongsTo(record, role) && record.Secret && seen && prior != record.Key {
					continue
				}
				if !seen {
					first[norm] = record.Key
				}
				kept = append(kept, record)
			}
			return kept
		}},
	}
}

// belongsTo is whether a record carries the slot role under test, by its
// normalized spelling, so a per-role adapter cannot be a no-op that passes for
// having changed nothing.
func belongsTo(record Record, convention lossRole) bool {
	return normalizeKey(record.Key) == normalizeKey(convention.upper)
}

// TestTheResolutionKitShipsExactlyTheseFixtures pins the resolution inventory
// by NAME, the way the two document kits are pinned.
//
// Without it, deleting five of a role's generated cases tripped nothing: the
// documented count was prose, the per-rule self-check was satisfied by another
// role's case, and I had claimed all three inventories were enforced when only
// the two document ones were. A changed resolution kit is a changed contract
// with every consumer that runs it, so it is declared here.
func TestTheResolutionKitShipsExactlyTheseFixtures(t *testing.T) {
	want := []string{
		"a binding key",
		"a binding key carrying a DEL",
		"a binding key carrying a newline",
		"a binding key carrying a paragraph separator",
		"a binding key carrying a zero-width space",
		"a binding key supplied public and secret",
		"a binding key supplied twice with different values",
		"a kind carrying a colon",
		"a kind carrying a comma",
		"a kind carrying a comma and a colon",
		"a kind carrying a line separator",
		"a public and a secret occurrence",
		"a resource kind",
		"a resource kind supplied public and secret",
		"a secret binding key",
		"a secret occurrence in the other spelling",
		"a secret resource kind",
		"a value that is not one line",
		"an audience carrying a DEL",
		"an audience carrying a NUL",
		"an audience carrying a byte-order mark",
		"an audience carrying a line separator",
		"an audience carrying a next line",
		"an audience carrying a word joiner",
		"an audience carrying a zero-width joiner",
		"an audience carrying a zero-width space",
		"an empty value",
		"an upper-case kind",
		"audience (_PREFIX convention): one spelling supplied twice with different values",
		"audience (_PREFIX convention): public then secret, a different value, both written lower",
		"audience (_PREFIX convention): public then secret, a different value, both written upper",
		"audience (_PREFIX convention): public then secret, a different value, public lower, secret upper",
		"audience (_PREFIX convention): public then secret, a different value, public upper, secret lower",
		"audience (_PREFIX convention): public then secret, the same value, both written lower",
		"audience (_PREFIX convention): public then secret, the same value, both written upper",
		"audience (_PREFIX convention): public then secret, the same value, public lower, secret upper",
		"audience (_PREFIX convention): public then secret, the same value, public upper, secret lower",
		"audience (_PREFIX convention): secret then public, a different value, both written lower",
		"audience (_PREFIX convention): secret then public, a different value, both written upper",
		"audience (_PREFIX convention): secret then public, a different value, public lower, secret upper",
		"audience (_PREFIX convention): secret then public, a different value, public upper, secret lower",
		"audience (_PREFIX convention): secret then public, the same value, both written lower",
		"audience (_PREFIX convention): secret then public, the same value, both written upper",
		"audience (_PREFIX convention): secret then public, the same value, public lower, secret upper",
		"audience (_PREFIX convention): secret then public, the same value, public upper, secret lower",
		"audience (_PREFIX convention): two spellings supplied with different values",
		"audience: one spelling supplied twice with different values",
		"audience: one secret record, upper spelling",
		"audience: one secret record, lower spelling",
		"resource_kind: one secret record, upper spelling",
		"resource_kind: one secret record, lower spelling",
		"binding_key: one secret record, upper spelling",
		"binding_key: one secret record, lower spelling",
		"audience (_PREFIX convention): one secret record, upper spelling",
		"audience (_PREFIX convention): one secret record, lower spelling",
		"audience: public then secret, a different value, both written lower",
		"audience: public then secret, a different value, both written upper",
		"audience: public then secret, a different value, public lower, secret upper",
		"audience: public then secret, a different value, public upper, secret lower",
		"audience: public then secret, the same value, both written lower",
		"audience: public then secret, the same value, both written upper",
		"audience: public then secret, the same value, public lower, secret upper",
		"audience: public then secret, the same value, public upper, secret lower",
		"audience: secret then public, a different value, both written lower",
		"audience: secret then public, a different value, both written upper",
		"audience: secret then public, a different value, public lower, secret upper",
		"audience: secret then public, a different value, public upper, secret lower",
		"audience: secret then public, the same value, both written lower",
		"audience: secret then public, the same value, both written upper",
		"audience: secret then public, the same value, public lower, secret upper",
		"audience: secret then public, the same value, public upper, secret lower",
		"audience: two spellings supplied with different values",
		"binding_key: one spelling supplied twice with different values",
		"binding_key: public then secret, a different value, both written lower",
		"binding_key: public then secret, a different value, both written upper",
		"binding_key: public then secret, a different value, public lower, secret upper",
		"binding_key: public then secret, a different value, public upper, secret lower",
		"binding_key: public then secret, the same value, both written lower",
		"binding_key: public then secret, the same value, both written upper",
		"binding_key: public then secret, the same value, public lower, secret upper",
		"binding_key: public then secret, the same value, public upper, secret lower",
		"binding_key: secret then public, a different value, both written lower",
		"binding_key: secret then public, a different value, both written upper",
		"binding_key: secret then public, a different value, public lower, secret upper",
		"binding_key: secret then public, a different value, public upper, secret lower",
		"binding_key: secret then public, the same value, both written lower",
		"binding_key: secret then public, the same value, both written upper",
		"binding_key: secret then public, the same value, public lower, secret upper",
		"binding_key: secret then public, the same value, public upper, secret lower",
		"binding_key: two spellings supplied with different values",
		"competing spellings that agree",
		"competing spellings that disagree",
		"no binding key at all",
		"no record at all",
		"no resource kind at all",
		"one record",
		"resource_kind: one spelling supplied twice with different values",
		"resource_kind: public then secret, a different value, both written lower",
		"resource_kind: public then secret, a different value, both written upper",
		"resource_kind: public then secret, a different value, public lower, secret upper",
		"resource_kind: public then secret, a different value, public upper, secret lower",
		"resource_kind: public then secret, the same value, both written lower",
		"resource_kind: public then secret, the same value, both written upper",
		"resource_kind: public then secret, the same value, public lower, secret upper",
		"resource_kind: public then secret, the same value, public upper, secret lower",
		"resource_kind: secret then public, a different value, both written lower",
		"resource_kind: secret then public, a different value, both written upper",
		"resource_kind: secret then public, a different value, public lower, secret upper",
		"resource_kind: secret then public, a different value, public upper, secret lower",
		"resource_kind: secret then public, the same value, both written lower",
		"resource_kind: secret then public, the same value, both written upper",
		"resource_kind: secret then public, the same value, public lower, secret upper",
		"resource_kind: secret then public, the same value, public upper, secret lower",
		"resource_kind: two spellings supplied with different values",
		"the other spelling",
		"the same record twice",
		"the same spelling supplied twice with different values",
	}
	have := map[string]bool{}
	for _, fixture := range AllResolutionFixtures() {
		have[fixture.Name] = true
	}
	missing := make([]string, 0, len(want))
	for _, name := range want {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("the resolution kit no longer ships %v; a dropped case is a dropped guarantee", missing)
	}
	if len(have) != len(want) {
		t.Errorf("the resolution kit ships %d configurations, not the %d declared here; a changed kit is a changed contract with every consumer that runs it",
			len(have), len(want))
	}
	for _, convention := range lossRoles() {
		count := 0
		for _, fixture := range AllResolutionFixtures() {
			if strings.HasPrefix(fixture.Name, convention.label+":") {
				count++
			}
		}
		if count != lossCasesPerRole {
			t.Errorf("the %s convention ships %d generated loss cases, not %d: a convention's protection cannot be deleted quietly",
				convention.label, count, lossCasesPerRole)
		}
	}
}

// lossCasesPerRole is what lossFixtures generates for one convention: two
// conflicts; then a SINGLE secret record in each spelling; then a
// public/secret collision over every order, every value agreement, and every
// INDEPENDENT choice of the two records' spellings — four spelling
// combinations, not two, since pairing them left no lowercase public record
// with an uppercase secret arriving later.
//
// The two singletons are the newest and the plainest: every collision case
// supplies TWO records for the slot's key, so a provider that cleared Secret
// only when exactly ONE record carried that key changed none of the
// configurations and was certified — and Resolve then succeeded with the
// secret's own value in a public field.
const lossCasesPerRole = 2 + 2 + 2*2*(2*2)

func TestTheDocumentedResolutionCountIsTheKit(t *testing.T) {
	const path = "../../docs/solution-host-binding.md"
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stated := fmt.Sprintf("`AllResolutionFixtures()` (%d", len(AllResolutionFixtures()))
	if !strings.Contains(string(document), stated) {
		t.Fatalf("%s does not state that the resolution kit ships %d configurations (looking for %q)",
			path, len(AllResolutionFixtures()), stated)
	}
}

// TestEveryResolvedFieldIsComparedByTheKit holds the kit's OUTPUT comparison
// to each field separately.
//
// NOT YET COVERED: resource_kind. Every substitute value tried is refused by
// the resolved-kind grammar before the comparison is reached, so protecting
// that field needs a value that is a DECLARED kind and action of the
// contract's own ceiling and still differs from the expected one. Until that
// exists, `kit-resource-kind-output-unchecked` is unprotected and is not
// claimed otherwise.
//
// The kit asserted the resolved binding whole, but nothing failed when one
// field's comparison was removed: a provider corrupting just that field kept
// a green verdict, because every other field still matched. So each field gets
// a provider that corrupts only it, and each must be caught.
func TestEveryResolvedFieldIsComparedByTheKit(t *testing.T) {
	for _, corrupt := range []struct {
		field, key, from, to string
	}{
		{fieldAudience, audienceKey, audienceValue, "assistant/other-audience"},
		{fieldBindingKey, bindingKey, bindingValue, "other-binding"},
	} {
		t.Run(corrupt.field, func(t *testing.T) {
			changed := 0
			recorder := &recordingT{}
			RunResolution(recorder, func(group string, records []Record) Values {
				kept := make([]Record, 0, len(records))
				for _, record := range records {
					if normalizeKey(record.Key) == corrupt.key && record.Value == corrupt.from && !record.Secret {
						record.Value = corrupt.to
						changed++
					}
					kept = append(kept, record)
				}
				return recordValues{group: group, records: kept}
			})
			if changed == 0 {
				t.Fatalf("no record carries %s: the probe changed nothing and proves nothing", corrupt.field)
			}
			// The failure must NAME this field. "Something failed" is
			// satisfied by any other fixture failing, so removing one
			// field's comparison left the coarse assertion green — which is
			// how the output comparison went unprotected in the first place.
			want := "must resolve " + corrupt.field + " to"
			if !strings.Contains(recorder.messages, want) {
				t.Fatalf("a provider corrupting only %s (in %d records) was not caught BY THAT FIELD's comparison; the kit reported:\n%s",
					corrupt.field, changed, recorder.messages)
			}
		})
	}
}

// TestTheKitRefusesAProviderItCannotBuild keeps the nil-factory guard honest:
// a factory returning no provider must fail the kit rather than be treated as
// an empty one, which would certify a consumer that never wired a provider.
func TestTheKitRefusesAProviderItCannotBuild(t *testing.T) {
	recorder := &recordingT{}
	RunResolution(recorder, nil)
	if recorder.failures == 0 {
		t.Fatal("a nil provider passed the resolution kit; it would be substituted by the reference provider and certify core against itself")
	}
}
