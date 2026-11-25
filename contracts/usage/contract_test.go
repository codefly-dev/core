package usage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
		guards := protected[r.name]
		if len(guards) == 0 {
			t.Errorf("rule %s is protected by no fixture: a reader could drop it and pass the kit", r.name)
			continue
		}
		if r.inherent {
			continue
		}
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			recorder := &recordingT{}
			Run(recorder, func(document []byte) error {
				_, err := parse(document, r.name)
				return err
			})
			if recorder.failures == 0 {
				t.Errorf("rule %s can be deleted and the kit still passes: %d fixture(s) name it but none notices", r.name, len(guards))
			}
			for _, fixture := range guards {
				_, err := parse(fixture.Document, r.name)
				if err != nil && strings.Contains(err.Error(), fixture.Message) {
					t.Errorf("fixture %s is still refused naming %q with rule %s deleted: the refusal comes from somewhere else", fixture.Name, fixture.Message, r.name)
				}
			}
		})
	}
}

// TestEveryFixtureReachesItsOutcome runs the kit against this package's own
// reader, and holds the kit to the files it ships: a document in testdata
// that no fixture names is a case nobody is held to.
func TestEveryFixtureReachesItsOutcome(t *testing.T) {
	Run(t, func(document []byte) error {
		_, err := Parse(document)
		return err
	})
	all, err := Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]bool{}
	for _, fixture := range all {
		if named[fixture.Name] {
			t.Errorf("fixture %s is listed twice", fixture.Name)
		}
		named[fixture.Name] = true
	}
	files, err := fs.Glob(fixtures, "testdata/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if name := strings.TrimSuffix(filepath.Base(file), ".yaml"); !named[name] {
			t.Errorf("%s is shipped and no fixture names it", file)
		}
	}
}

// TestTheKitFailsAReaderThatSkipsARule: a reader that ignores unknown fields
// passes the accepted fixtures and fails the refusal by name, which is what
// makes the kit a gate rather than a round-trip.
func TestTheKitFailsAReaderThatSkipsARule(t *testing.T) {
	recorder := &recordingT{}
	Run(recorder, func(document []byte) error {
		_, err := parse(document, ruleKnownFields)
		return err
	})
	if recorder.failures == 0 || !strings.Contains(recorder.messages, "usage contract fixture carries-a-command must be refused") {
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

func (r *recordingT) Fatalf(format string, args ...any) { r.Errorf(format, args...) }

func full(t *testing.T) *Contract {
	t.Helper()
	data, err := fixtures.ReadFile("testdata/" + fullFixture + ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func TestParsesTheShapeAnOwnerPublishes(t *testing.T) {
	contract := full(t)
	if len(contract.Rules) != 2 || contract.Summary == "" {
		t.Fatalf("got %+v", contract)
	}
	conn := contract.Rule("STORE-CONN-001")
	if conn == nil || conn.Severity != SeverityError || conn.Since != "1.4.0" || conn.Docs != "docs/usage.md" {
		t.Fatalf("got %+v", conn)
	}
	if clause := conn.Detects("go"); clause == nil ||
		!slices.Equal(clause.ForbidCall, []string{"database/sql.Open"}) ||
		!slices.Equal(clause.ForbidImport, []string{"github.com/example/rawdriver"}) {
		t.Fatalf("the go clause: %+v", clause)
	}
	if clause := conn.Detects("python"); clause == nil || !slices.Equal(clause.ForbidCall, []string{"psycopg.connect"}) {
		t.Fatalf("the python clause: %+v", clause)
	}
	if !slices.Equal(conn.ExceptPaths, []string{"**/migrationtest/**"}) {
		t.Fatalf("except paths: %v", conn.ExceptPaths)
	}
}

// A rule with no clause for a language still applies to it: only no linter
// checks it. A reader tells the two apart by a nil clause, never by an error.
func TestARuleWithoutAClauseForALanguageDetectsNothingThere(t *testing.T) {
	contract := full(t)
	if clause := contract.Rule("STORE-CONN-001").Detects("rust"); clause != nil {
		t.Fatalf("no clause is declared for rust, got %+v", clause)
	}
	maint := contract.Rule("STORE-MAINT-001")
	if maint == nil || len(maint.Detect) != 0 || maint.Severity != SeverityWarn {
		t.Fatalf("the judgement rule: %+v", maint)
	}
	if contract.Rule("STORE-NONE-001") != nil {
		t.Fatal("an undeclared id names no rule")
	}
}

func TestTheModelWritesWhatItReads(t *testing.T) {
	contract := full(t)
	encoded, err := contract.Encode()
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(encoded)
	if err != nil {
		t.Fatalf("the encoded contract does not parse: %v\n%s", err, encoded)
	}
	reencoded, err := again.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(reencoded) {
		t.Fatalf("encoding is not stable:\n%s\nthen:\n%s", encoded, reencoded)
	}
}

// A contract built in memory is held to the same rules as one read from a
// file, so nothing a reader would refuse can be written.
func TestEncodeRefusesWhatParseWouldRefuse(t *testing.T) {
	contract := full(t)
	contract.Rules[0].Detect[0].ForbidCall = []string{"Open"}
	if _, err := contract.Encode(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), `forbids the go call "Open"`) {
		t.Fatalf("got %v", err)
	}
	var none *Contract
	if err := none.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a nil contract is invalid, got %v", err)
	}
}

func TestLoadReadsTheFileBesideAManifest(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an owner with no contract is os.ErrNotExist, got %v", err)
	}
	data, err := fixtures.ReadFile("testdata/judgement-only.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	contract, err := Load(dir)
	if err != nil || contract.Rule("TASKS-RETRY-001") == nil {
		t.Fatalf("got %+v, %v", contract, err)
	}
	refused, err := fixtures.ReadFile("testdata/no-do.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), refused, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), FileName) {
		t.Fatalf("an unreadable contract names its file, got %v", err)
	}
}

// A key repeated at the top of the file is refused by the rule that owns
// repeated keys, and a file of another schema is a version skew whatever else
// is wrong with it: neither is reported as a file that is not YAML.
func TestTheTopOfTheFileIsReadBeforeItIsDecoded(t *testing.T) {
	twice := "schema: codefly/usage/v1\nsummary: One.\nsummary: Two.\nrules: []\n"
	if _, err := Parse([]byte(twice)); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), `names the key "summary" twice`) {
		t.Fatalf("got %v", err)
	}
	skewed := "schema: codefly/usage/v2\nsummary: One.\nsummary: Two.\n"
	if _, err := Parse([]byte(skewed)); !errors.Is(err, ErrSchema) {
		t.Fatalf("got %v", err)
	}
	for _, document := range []string{"", "- a\n", "summary: no schema\n", "schema: [a]\n"} {
		if _, err := Parse([]byte(document)); !errors.Is(err, ErrSchema) {
			t.Errorf("%q declares no schema this reader reads, got %v", document, err)
		}
	}
}

// What a symbol may be, case by case: a literal, qualified name in any of
// the spellings the languages use, and never a pattern.
func TestSymbolsAreLiteralQualifiedNames(t *testing.T) {
	for symbol, want := range map[string]bool{
		"database/sql.Open":                      true,
		"github.com/jackc/pgx/v5/pgxpool.New":    true,
		"psycopg.connect":                        true,
		"asyncpg.pool.create_pool":               true,
		"@prisma/client.PrismaClient":            true,
		"std::net::TcpStream:connect":            false,
		"node:net.connect":                       true,
		"Open":                                   false,
		"database/sql.*":                         false,
		"database/sql.":                          false,
		".Open":                                  false,
		"database/sql.Open()":                    false,
		"database/sql.Open ":                     false,
		"pgx.(*Conn).Exec":                       false,
		"github.com/jackc/pgx/v5/pgxpool.New\nx": false,
	} {
		if got := callName.MatchString(symbol); got != want {
			t.Errorf("call %q: accepted %v, want %v", symbol, got, want)
		}
	}
	for symbol, want := range map[string]bool{
		"github.com/example/rawdriver": true,
		"psycopg":                      true,
		"@prisma/client":               true,
		"node:net":                     true,
		"github.com/example/*":         false,
		"":                             false,
		"../sibling":                   false,
		"a b":                          false,
	} {
		if got := importName.MatchString(symbol); got != want {
			t.Errorf("import %q: accepted %v, want %v", symbol, got, want)
		}
	}
}

// TestTheDocumentedCountsAreTheKits: docs/usage-contract.md states how many
// rules the reader holds and how many documents the kit ships. A count in
// prose that nothing checks is a count that is wrong.
func TestTheDocumentedCountsAreTheKits(t *testing.T) {
	all, err := Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "docs", "usage-contract.md")
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stated := range []string{
		fmt.Sprintf("`usagecontract.Fixtures()` (%d", len(all)),
		fmt.Sprintf("%d named rules", len(rules())),
	} {
		if !strings.Contains(string(document), stated) {
			t.Errorf("%s does not state %q", path, stated)
		}
	}
	// Every rule is named in the document's table, so a rule added without
	// its line there is caught.
	names := ruleNames()
	sort.Strings(names)
	for _, name := range names {
		if !strings.Contains(string(document), "`"+name+"`") {
			t.Errorf("%s does not name the rule %s", path, name)
		}
	}
}
