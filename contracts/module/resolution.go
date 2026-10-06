package module

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Record is one workspace configuration entry exactly as the composition
// supplies it: the key AS WRITTEN, its value, and whether the composition
// classified it secret.
type Record struct {
	// Key is the key as the composition writes it, in whichever spelling.
	Key string
	// Value is the value supplied for it.
	Value string
	// Secret is whether the composition classified it secret.
	Secret bool
}

// Values is where a contract's slots resolve from: a provider enumerates the
// records of one workspace configuration group, as supplied, KEEPING every
// record — both spellings of one key, a key supplied twice, a key classified
// two ways.
//
// A provider does not select among them. Normalisation, ambiguity and secret
// precedence are resolution RULES, and they live in this package, once, beside
// the rules that refuse a document: a provider that selected would be a second
// implementation of this contract — and the one a renderer actually runs — so
// two consumers could resolve one composition into two different authority
// documents while both passed the document kit. This shape forecloses that,
// because a provider never decides anything; RunResolution is what fails a
// provider that discards a record on the way here.
type Values interface {
	Records(group string) ([]Record, error)
}

// The resolution rules, by name — the refusals of the resolution half, each
// addressable by the kit exactly as a document rule is.
const (
	// ruleOneValue: records that are one key must agree on its value.
	ruleOneValue = "one-value"
	// ruleSecretPrecedence: a key supplied as both public and secret IS the
	// secret. The stricter reading of one key, decided here rather than by a
	// provider: the executed review found one adapter keeping such a key
	// public while core read it as secret, which is a value reaching a
	// delivered document in one consumer and refused in another.
	ruleSecretPrecedence = "secret-precedence"
	// ruleResolvedKind: a resolved resource kind is held to the grammar a
	// literal resource kind is held to.
	ruleResolvedKind = "resolved-kind-grammar"
	// ruleResolvedName: a resolved value is one non-empty line.
	ruleResolvedName = "resolved-single-line"
)

// resolutionRuleNames is every rule of the resolution half, in apply order.
func resolutionRuleNames() []string {
	return []string{ruleSecretPrecedence, ruleOneValue, ruleResolvedName, ruleResolvedKind}
}

// resolution is what the rules answer for one slot: the value, or the reason
// no value resolves.
type resolution struct {
	value  string
	secret bool
	found  bool
	reason string
}

// resolveSlot is the ONE resolution of one slot against a group's records. It
// is the whole policy: which records are that key, whether they agree, which
// classification holds, and whether the value that comes out is one a scope or
// an audience can be built from. `kind` marks the slot whose value names a
// resource kind, which is held to the literal-kind grammar — without which a
// composition supplying "documents.passages:delete,documents.passages" turns
// one declared action into two apparent scopes. `without` removes one rule,
// for the kit's completeness self-check.
func resolveSlot(records []Record, key string, kind bool, without string) resolution {
	normalized := normalizeKey(key)
	var matched []Record
	for _, record := range records {
		if normalizeKey(record.Key) == normalized {
			matched = append(matched, record)
		}
	}
	if len(matched) == 0 {
		return resolution{}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Key != matched[j].Key {
			return matched[i].Key < matched[j].Key
		}
		return !matched[i].Secret && matched[j].Secret
	})
	spellings := strings.Join(distinct(matched, func(record Record) string { return record.Key }), " and ")

	// Secret precedence first, and before the values are even compared: a key
	// any occurrence of which is secret is a secret, so no secret's value is
	// quoted in a refusal and no provider's ordering can make it public.
	if slices.ContainsFunc(matched, func(record Record) bool { return record.Secret }) && without != ruleSecretPrecedence {
		return resolution{value: matched[0].Value, secret: true, found: true}
	}
	if values := distinct(matched, func(record Record) string { return record.Value }); len(values) > 1 && without != ruleOneValue {
		return resolution{reason: fmt.Sprintf("is supplied as %s, which core reads as one key, so there is no one value to resolve", spellings)}
	}
	first := matched[0]
	if !singleLine(first.Value) && without != ruleResolvedName {
		return resolution{reason: "resolves to a value that is empty or not a single line"}
	}
	if kind && !namePattern.MatchString(first.Value) && without != ruleResolvedKind {
		// A resolved kind is concatenated into "<kind>:<action>", so a comma
		// or a colon in it carries scopes the contract never declared.
		return resolution{reason: fmt.Sprintf("resolves to %q, which is not a lowercase resource kind; a resolved kind is held to the grammar a literal one is, because it is concatenated into a scope", first.Value)}
	}
	return resolution{value: first.Value, found: true}
}

// singleLine holds a resolved value to one line, under a UNICODE policy and
// not an ASCII one. The predicate used to exclude ASCII controls, space and
// DEL only, and TrimSpace looks at the ends — so NEXT LINE (U+0085), LINE
// SEPARATOR (U+2028) and PARAGRAPH SEPARATOR (U+2029) passed embedded in an
// audience or a binding key and survived into the resolved output, which is a
// line break in a value this contract says is one line. Format characters go
// with them: an invisible character in a name a receiver matches on is the
// same defect without the line break.
func singleLine(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

func distinct(records []Record, of func(Record) string) []string {
	var seen []string
	for _, record := range records {
		if value := of(record); !slices.Contains(seen, value) {
			seen = append(seen, value)
		}
	}
	sort.Strings(seen)
	return seen
}

// MapValues is a Values over nested maps: group → key → value, with the keys
// of secrets listed separately. It is a convenience for a composition already
// held that way; it selects nothing, and reports both maps' entries as the
// records they are.
type MapValues struct {
	Public  map[string]map[string]string
	Secrets map[string]map[string]string
}

// Records implements Values.
func (values MapValues) Records(group string) ([]Record, error) {
	records := make([]Record, 0, len(values.Public[group])+len(values.Secrets[group]))
	for key, value := range values.Public[group] {
		records = append(records, Record{Key: key, Value: value})
	}
	for key, value := range values.Secrets[group] {
		records = append(records, Record{Key: key, Value: value, Secret: true})
	}
	return records, nil
}

func normalizeKey(key string) string {
	return strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
}
