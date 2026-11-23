package usage

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
	// OutcomeAccepted means the contract parses and validates.
	OutcomeAccepted Outcome = "accepted"
	// OutcomeRefused means the contract is refused with the fixture's
	// Sentinel, and a message carrying its Message.
	OutcomeRefused Outcome = "refused"
)

// Fixture is one document of the conformance kit and the verdict every
// reader must reach on it.
type Fixture struct {
	// Name identifies the fixture in a failure.
	Name string
	// Document is the file's bytes, as an owner would publish them.
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

// fullFixture is the accepted document the bound fixtures are grown from.
const fullFixture = "full"

// Fixtures is the kit: every accepted and refused document, with the verdict
// each must reach. Every rule the reader enforces is protected by at least
// one refused fixture here, which the package's own tests prove by deleting
// each rule in turn.
//
// A fixture that exceeds a bound is grown from the accepted "full" document
// rather than shipped as a file, so the kit does not carry a quarter of a
// megabyte into every binary that imports it.
func Fixtures() ([]Fixture, error) {
	table := []struct {
		name, message, rule string
		outcome             Outcome
		sentinel            error
	}{
		{name: fullFixture, outcome: OutcomeAccepted},
		{name: "judgement-only", outcome: OutcomeAccepted},
		{name: "package-named", outcome: OutcomeAccepted},

		{name: "not-yaml", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleWellFormed, message: "is not a YAML document"},
		{name: "another-schema", outcome: OutcomeRefused, sentinel: ErrSchema, rule: ruleSchema, message: `"codefly/usage/v2"`},
		{name: "key-declared-twice", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleMappingKeysOnce, message: `names the key "title" twice`},
		{name: "merge-key", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleNoMergeKeys, message: "uses the merge key <<"},
		{name: "explicit-null", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleNoNulls, message: "explicit null"},
		{name: "since-a-number", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleWholeNumbers, message: "the number 1.4"},
		{name: "key-not-a-name", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleKeyIsAName, message: "a key that is not a name"},
		{name: "carries-a-command", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleKnownFields, message: "a field no contract declares"},
		{name: "two-documents", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleOneDocument, message: "more than one document"},

		{name: "no-summary", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleSummary, message: "summary is required"},
		{name: "no-rules", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleRulesDeclared, message: "states at least one rule"},
		{name: "id-lower-case", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleIDShape, message: `has the id "store-conn-1"`},
		{name: "id-declared-twice", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleIDsUnique, message: "STORE-CONN-001 is declared twice"},
		{name: "no-title", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleTitle, message: "has no title"},
		{name: "title-of-two-lines", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleTitle, message: "a title of more than one line"},
		{name: "severity-unknown", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleSeverity, message: `has the severity "fatal"`},
		{name: "since-with-a-v", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleSince, message: `has since "v1.5.0"`},
		{name: "no-rationale", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleRationale, message: "has no rationale"},
		{name: "no-do", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleDo, message: "has no do"},
		{name: "summary-with-a-hidden-character", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleTextPrintable, message: "the non-printing character U+200B"},
		{name: "docs-a-url", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleDocsRelative, message: `has docs "https://example.com/usage.md"`},
		{name: "docs-leaves-the-release", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleDocsRelative, message: `has docs "../other/usage.md"`},
		{name: "language-capitalised", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleDetectLanguage, message: `for the language "Python"`},
		{name: "language-twice", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleDetectOnePerLang, message: "two detect clauses for go"},
		{name: "detect-forbids-nothing", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleDetectForbids, message: "for python that forbids nothing"},
		{name: "call-bare-name", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleCallShape, message: `forbids the python call "connect"`},
		{name: "call-a-pattern", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleCallShape, message: `forbids the go call "database/sql.*"`},
		{name: "import-a-pattern", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleImportShape, message: `forbids the go import "github.com/example/*"`},
		{name: "call-listed-twice", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleSymbolsUnique, message: `lists "psycopg.connect" twice`},
		{name: "name-owns-no-call", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleNamesOwnACall, message: `names "example.com/other"`},
		{name: "name-not-an-identifier", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleNamesAreNames, message: `is written "store.v2"`},
		{name: "except-paths-without-detect", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleExceptNeedsDetect, message: "has except-paths and no detect clause"},
		{name: "except-path-absolute", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleExceptRelative, message: `excepts the path "/etc/**"`},
		{name: "except-path-leaves-the-service", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleExceptRelative, message: `excepts the path "../sibling/**"`},
		{name: "except-path-twice", outcome: OutcomeRefused, sentinel: ErrInvalid, rule: ruleExceptUnique, message: `excepts the path "**/migrationtest/**" twice`},
	}
	out := make([]Fixture, 0, len(table)+len(grown))
	var full string
	for _, row := range table {
		data, err := fixtures.ReadFile("testdata/" + row.name + ".yaml")
		if err != nil {
			return nil, err
		}
		if row.name == fullFixture {
			full = string(data)
		}
		out = append(out, Fixture{Name: row.name, Document: data, Outcome: row.outcome, Sentinel: row.sentinel, Message: row.message, Rule: row.rule})
	}
	for _, row := range grown {
		document, err := row.grow(full)
		if err != nil {
			return nil, fmt.Errorf("usage contract fixture %s: %w", row.name, err)
		}
		out = append(out, Fixture{Name: row.name, Document: []byte(document), Outcome: OutcomeRefused, Sentinel: ErrInvalid, Message: row.message, Rule: row.rule})
	}
	return out, nil
}

// replaced is full with one occurrence of old replaced, and an error when the
// accepted document no longer carries it: a bound fixture that silently
// stopped exceeding its bound would protect nothing.
func replaced(full, old, replacement string) (string, error) {
	if !strings.Contains(full, old) {
		return "", fmt.Errorf("the accepted document no longer carries %q", old)
	}
	return strings.Replace(full, old, replacement, 1), nil
}

// list is n comma-separated entries, each made from its index.
func list(n int, entry func(i int) string) string {
	entries := make([]string, n)
	for i := range entries {
		entries[i] = entry(i)
	}
	return strings.Join(entries, ", ")
}

// grown are the fixtures that exceed a bound by one.
var grown = []struct {
	name, message, rule string
	grow                func(full string) (string, error)
}{
	{name: "document-too-large", rule: ruleDocumentBounded, message: "a contract is at most", grow: func(full string) (string, error) {
		return full + "# " + strings.Repeat("x", MaxDocumentBytes) + "\n", nil
	}},
	{name: "too-many-rules", rule: ruleRulesBounded, message: "a contract states at most", grow: func(full string) (string, error) {
		var more strings.Builder
		for i := 0; i < MaxRules-1; i++ {
			fmt.Fprintf(&more, "  - id: STORE-MORE-%d\n    title: One more rule\n    severity: warn\n    since: 1.5.0\n    rationale: It is one more.\n    do: Count them.\n", i)
		}
		return full + more.String(), nil
	}},
	{name: "summary-too-long", rule: ruleTextBounded, message: "summary is", grow: func(full string) (string, error) {
		return replaced(full, "summary: Reach", "summary: "+strings.Repeat("x", MaxSummaryRunes)+" Reach")
	}},
	{name: "too-many-detect-clauses", rule: ruleDetectBounded, message: "detect clauses; a rule has at most", grow: func(full string) (string, error) {
		var more strings.Builder
		for i := 0; i < MaxDetectClauses-1; i++ {
			fmt.Fprintf(&more, "      - language: lang%d\n        forbid-import: [example]\n", i)
		}
		return replaced(full, "    except-paths:", more.String()+"    except-paths:")
	}},
	{name: "too-many-symbols", rule: ruleSymbolsBounded, message: "symbols; a list has at most", grow: func(full string) (string, error) {
		return replaced(full, "forbid-call: [psycopg.connect]", "forbid-call: ["+list(MaxSymbols+1, func(i int) string { return fmt.Sprintf("psycopg.connect%d", i) })+"]")
	}},
	{name: "too-many-except-paths", rule: ruleExceptBounded, message: "except-paths; a rule has at most", grow: func(full string) (string, error) {
		return replaced(full, `except-paths: ["**/migrationtest/**"]`, "except-paths: ["+list(MaxExceptPaths+1, func(i int) string { return fmt.Sprintf(`"tests%d/**"`, i) })+"]")
	}},
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
// actually reads contracts with — an agent's loader, the linter's resolver —
// never Parse itself, which proves nothing about the consumer.
func Run(t TestingT, read func(document []byte) error) {
	t.Helper()
	if read == nil {
		t.Fatalf("usage contract conformance: no entrypoint given")
		return
	}
	all, err := Fixtures()
	if err != nil {
		t.Fatalf("usage contract conformance: %v", err)
		return
	}
	for _, fixture := range all {
		err := read(fixture.Document)
		switch fixture.Outcome {
		case OutcomeAccepted:
			if err != nil {
				t.Errorf("usage contract fixture %s must be accepted, got: %v", fixture.Name, err)
			}
		case OutcomeRefused:
			switch {
			case err == nil:
				t.Errorf("usage contract fixture %s must be refused with %v (rule %s)", fixture.Name, fixture.Sentinel, fixture.Rule)
			case !errors.Is(err, fixture.Sentinel):
				t.Errorf("usage contract fixture %s must be refused with %v (rule %s), got: %v", fixture.Name, fixture.Sentinel, fixture.Rule, err)
			case !strings.Contains(err.Error(), fixture.Message):
				t.Errorf("usage contract fixture %s must be refused naming %q (rule %s), got: %v", fixture.Name, fixture.Message, fixture.Rule, err)
			}
		}
	}
}
