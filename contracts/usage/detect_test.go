package usage

import (
	"reflect"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// source is a consumer's semantic index, written the way an analyzer
// projects it: imports per file, calls as written inside declarations.
type source struct {
	path, language string
	imports        []string
	calls          []string
}

func indexOf(files ...source) *basev0.SemanticIndex {
	index := &basev0.SemanticIndex{}
	for _, file := range files {
		index.Files = append(index.Files, &basev0.SemanticFile{Path: file.path, Language: file.language, Imports: file.imports})
		symbol := &basev0.SemanticSymbol{Name: "f", Location: &basev0.SemanticLocation{Path: file.path}}
		for line, call := range file.calls {
			symbol.Calls = append(symbol.Calls, &basev0.SemanticUse{Name: call, Location: &basev0.SemanticLocation{Path: file.path, StartLine: int32(line + 10), StartColumn: 2}})
		}
		index.Symbols = append(index.Symbols, symbol)
	}
	return index
}

func call(rule, language, symbol, path string, line int32) Finding {
	return Finding{RuleID: rule, Severity: SeverityError, Language: language, Kind: FindingCall, Symbol: symbol, Path: path, Line: line, Column: 2}
}

func imported(rule, language, symbol, path string) Finding {
	return Finding{RuleID: rule, Severity: SeverityError, Language: language, Kind: FindingImport, Symbol: symbol, Path: path}
}

func TestDetectFindsWhatTheFullContractForbids(t *testing.T) {
	contract := full(t)
	findings := contract.Detect(indexOf(
		source{path: "pkg/store/open.go", language: "go", imports: []string{"context", "database/sql"}, calls: []string{"context.Background", "sql.Open", "fmt.Println"}},
		source{path: "pkg/store/raw.go", language: "go", imports: []string{"github.com/example/rawdriver"}},
		source{path: "pkg/store/sub.go", language: "go", imports: []string{"github.com/example/rawdriver/pool"}},
		source{path: "pkg/store/clean.go", language: "go", imports: []string{"database/sql"}, calls: []string{"sql.Named"}},
		source{path: "pkg/migrationtest/setup.go", language: "go", imports: []string{"database/sql"}, calls: []string{"sql.Open"}},
		source{path: "app/db.py", language: "python", imports: []string{"psycopg"}, calls: []string{"psycopg.connect", "psycopg.connect"}},
		source{path: "app/main.rs", language: "rust", imports: []string{"sqlx::query"}, calls: []string{"sqlx::connect"}},
	))
	want := []Finding{
		call("STORE-CONN-001", "python", "psycopg.connect", "app/db.py", 10),
		call("STORE-CONN-001", "python", "psycopg.connect", "app/db.py", 11),
		call("STORE-CONN-001", "go", "database/sql.Open", "pkg/store/open.go", 11),
		imported("STORE-CONN-001", "go", "github.com/example/rawdriver", "pkg/store/raw.go"),
		imported("STORE-CONN-001", "go", "github.com/example/rawdriver", "pkg/store/sub.go"),
	}
	if !reflect.DeepEqual(findings, want) {
		t.Fatalf("got  %+v\nwant %+v", findings, want)
	}
}

// One clause, one file, one case each: what is reported and what is
// deliberately not.
func TestWhatACallMatches(t *testing.T) {
	for name, c := range map[string]struct {
		forbidden string
		file      source
		found     bool
	}{
		"the package's own name":            {"github.com/jackc/pgx/v5/pgxpool.New", source{language: "go", imports: []string{"github.com/jackc/pgx/v5/pgxpool"}, calls: []string{"pgxpool.New"}}, true},
		"a major-version path element":      {"github.com/jackc/pgx/v5.Connect", source{language: "go", imports: []string{"github.com/jackc/pgx/v5"}, calls: []string{"pgx.Connect"}}, true},
		"a gopkg.in version suffix":         {"gopkg.in/yaml.v3.Unmarshal", source{language: "go", imports: []string{"gopkg.in/yaml.v3"}, calls: []string{"yaml.Unmarshal"}}, true},
		"a dotted python module":            {"asyncpg.pool.create_pool", source{language: "python", imports: []string{"asyncpg.pool"}, calls: []string{"asyncpg.pool.create_pool"}}, true},
		"a dotted call through its parent":  {"asyncpg.pool.create_pool", source{language: "python", imports: []string{"asyncpg"}, calls: []string{"asyncpg.pool.create_pool"}}, true},
		"a submodule by its own name":       {"asyncpg.pool.create_pool", source{language: "python", imports: []string{"asyncpg.pool"}, calls: []string{"pool.create_pool"}}, true},
		"another function of the owner":     {"database/sql.Open", source{language: "go", imports: []string{"database/sql"}, calls: []string{"sql.OpenDB"}}, false},
		"the same name, owner not imported": {"database/sql.Open", source{language: "go", imports: []string{"example.com/own/sql"}, calls: []string{"sql.Open"}}, false},
		"a same-named import beside it":     {"database/sql.Open", source{language: "go", imports: []string{"database/sql", "example.com/own/sql"}, calls: []string{"sql.Open"}}, false},
		"an import alias is missed":         {"database/sql.Open", source{language: "go", imports: []string{"database/sql"}, calls: []string{"stdsql.Open"}}, false},
		"a bare imported name is missed":    {"psycopg.connect", source{language: "python", imports: []string{"psycopg"}, calls: []string{"connect"}}, false},
		"a method on a value is not a call": {"database/sql.Open", source{language: "go", imports: []string{"database/sql"}, calls: []string{"s.sql.Open"}}, false},
		"another language's file":           {"database/sql.Open", source{language: "python", imports: []string{"database/sql"}, calls: []string{"sql.Open"}}, false},
		"a dotted go path element is left":  {"example.com/a.b.New", source{language: "go", imports: []string{"example.com/a.b"}, calls: []string{"b.New"}}, false},
	} {
		// The rule has a clause for one language only, so "another language's
		// file" is a python file read against a go clause.
		clause := c.file.language
		if name == "another language's file" {
			clause = "go"
		}
		contract := &Contract{Schema: SchemaV1, Summary: "s", Rules: []Rule{{
			ID: "X-Y-1", Title: "t", Severity: SeverityError, Since: "1.0.0", Rationale: "r", Do: "d",
			Detect: []Detect{{Language: clause, ForbidCall: []string{c.forbidden}}},
		}}}
		c.file.path = "a/file"
		if err := contract.Validate(); err != nil {
			t.Fatalf("%s: the case's own contract is invalid: %v", name, err)
		}
		if got := len(contract.Detect(indexOf(c.file))) > 0; got != c.found {
			t.Errorf("%s: found %v, want %v", name, got, c.found)
		}
	}
}

// A package whose path does not end in its name is matched by the name its
// owner declares, and then only by that name.
func TestAPackageIsMatchedByTheNameItsOwnerDeclares(t *testing.T) {
	data, err := fixtures.ReadFile("testdata/package-named.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	const library = "example.com/store/libs/go"
	for written, found := range map[string]bool{"store.NewFactory": true, "go.NewFactory": false, "store.Open": false} {
		findings := contract.Detect(indexOf(source{path: "a.go", language: "go", imports: []string{library}, calls: []string{written}}))
		if got := len(findings) == 1 && findings[0].Symbol == library+".NewFactory"; got != found {
			t.Errorf("%s: found %v, want %v (%+v)", written, got, found, findings)
		}
	}
	// A consumer's own package of the same name, imported beside the library
	// (which it then has to alias), makes postgres.NewFactory its own call as
	// likely as the library's. Found on a real consumer; it is missed, not
	// reported.
	if findings := contract.Detect(indexOf(source{path: "a.go", language: "go", imports: []string{library, "example.com/consumer/pkg/store"}, calls: []string{"store.NewFactory"}})); len(findings) != 0 {
		t.Errorf("an ambiguous qualifier was reported: %+v", findings)
	}
	// The name is the owner's package, not a licence to match the name
	// anywhere: without the import there is nothing to find.
	if findings := contract.Detect(indexOf(source{path: "a.go", language: "go", imports: []string{"example.com/own/store"}, calls: []string{"store.NewFactory"}})); len(findings) != 0 {
		t.Errorf("found without the import: %+v", findings)
	}
}

func TestWhatAnImportMatches(t *testing.T) {
	for name, c := range map[string]struct {
		forbidden, imported string
		found               bool
	}{
		"the package itself":          {"github.com/lib/pq", "github.com/lib/pq", true},
		"a package beneath it":        {"github.com/lib/pq", "github.com/lib/pq/oid", true},
		"a python submodule":          {"psycopg", "psycopg.rows", true},
		"a rust path":                 {"sqlx", "sqlx::postgres::PgPool", true},
		"a longer name is another":    {"github.com/lib/pq", "github.com/lib/pqx", false},
		"a parent is not the package": {"github.com/lib/pq/oid", "github.com/lib/pq", false},
		"a prefix of a python name":   {"psycopg", "psycopg2", false},
	} {
		if got := beneath(c.imported, c.forbidden); got != c.found {
			t.Errorf("%s: found %v, want %v", name, got, c.found)
		}
	}
}

func TestExceptedPathsAreSkippedForTheirRuleOnly(t *testing.T) {
	for glob, cases := range map[string]map[string]bool{
		"**/migrationtest/**": {"pkg/migrationtest/a.go": true, "migrationtest/a.go": true, "pkg/migrationtest/deep/a.go": true, "pkg/migrationtests/a.go": false, "pkg/a.go": false},
		"**/*_test.go":        {"a_test.go": true, "pkg/deep/a_test.go": true, "pkg/a_test.gox": false, "pkg/a.go": false},
		"tests/**":            {"tests/a.py": true, "tests/deep/a.py": true, "src/tests/a.py": false},
		"cmd/*/main.go":       {"cmd/tool/main.go": true, "cmd/tool/sub/main.go": false, "cmd/main.go": false},
		"a?.go":               {"ab.go": true, "a/.go": false, "abc.go": false},
		"a.go":                {"a.go": true, "axgo": false},
	} {
		excepted := exceptions([]string{glob})
		for path, want := range cases {
			if got := excepted(path); got != want {
				t.Errorf("%q against %q: excepted %v, want %v", path, glob, got, want)
			}
		}
	}
}

func TestDetectReadsNothingItWasNotGiven(t *testing.T) {
	contract := full(t)
	if findings := contract.Detect(nil); findings != nil {
		t.Fatalf("no index, no findings: %+v", findings)
	}
	var none *Contract
	if findings := none.Detect(indexOf()); findings != nil {
		t.Fatalf("no contract, no findings: %+v", findings)
	}
	judgement := &Contract{Schema: SchemaV1, Summary: "s", Rules: []Rule{*contract.Rule("STORE-MAINT-001")}}
	if findings := judgement.Detect(indexOf(source{path: "a.go", language: "go", imports: []string{"database/sql"}, calls: []string{"sql.Open"}})); findings != nil {
		t.Fatalf("a rule with no detect clause finds nothing: %+v", findings)
	}
}

// A call outside every function is on its file, and is found like any other.
func TestDetectReadsCallsOutsideFunctions(t *testing.T) {
	contract := full(t)
	index := &basev0.SemanticIndex{
		FileCallsProjected: true,
		Files: []*basev0.SemanticFile{{
			Path: "app/db.py", Language: "python", Imports: []string{"psycopg"},
			Calls: []*basev0.SemanticUse{{Name: "psycopg.connect", Location: &basev0.SemanticLocation{Path: "app/db.py", StartLine: 3, StartColumn: 14}}},
		}},
	}
	want := []Finding{{RuleID: "STORE-CONN-001", Severity: SeverityError, Language: "python", Kind: FindingCall, Symbol: "psycopg.connect", Path: "app/db.py", Line: 3, Column: 14}}
	if findings := contract.Detect(index); !reflect.DeepEqual(findings, want) {
		t.Fatalf("got  %+v\nwant %+v", findings, want)
	}
	if unread := contract.Unread(index); len(unread) != 0 {
		t.Fatalf("an index that projected file calls leaves nothing unread: %v", unread)
	}
}

// An index whose analyzer did not project calls outside functions leaves
// those places unread, and says so only to a contract that could have
// matched something there.
func TestUnreadSaysWhenInitializersWereNotProjected(t *testing.T) {
	contract := full(t)
	old := func(language string) *basev0.SemanticIndex {
		return &basev0.SemanticIndex{Files: []*basev0.SemanticFile{{Path: "a", Language: language}}}
	}
	if unread := contract.Unread(old("go")); len(unread) != 1 || !strings.Contains(unread[0], "calls outside functions and methods") {
		t.Fatalf("a go clause forbids a call and go initializers went unread: %v", unread)
	}
	// The full contract has no clause for rust, so nothing it could match
	// was missed in a rust-only service.
	if unread := contract.Unread(old("rust")); len(unread) != 0 {
		t.Fatalf("no clause for the language, nothing unread: %v", unread)
	}
	// A contract that forbids only imports reads no call at all.
	importsOnly := &Contract{Schema: SchemaV1, Summary: "s", Rules: []Rule{{
		ID: "X-Y-1", Title: "t", Severity: SeverityError, Since: "1.0.0", Rationale: "r", Do: "d",
		Detect: []Detect{{Language: "go", ForbidImport: []string{"github.com/lib/pq"}}},
	}}}
	if unread := importsOnly.Unread(old("go")); len(unread) != 0 {
		t.Fatalf("imports are always read: %v", unread)
	}
	var none *Contract
	if none.Unread(old("go")) != nil || contract.Unread(nil) != nil {
		t.Fatal("nothing given, nothing unread")
	}
}
