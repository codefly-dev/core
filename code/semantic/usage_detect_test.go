package semantic

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	usagecontract "github.com/codefly-dev/core/contracts/usage"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// A usage contract's detect clauses are matched against the index this
// analyzer produces, so the two are proven together here on real source: the
// matcher's own tests write an index by hand and cannot notice the analyzer
// spelling a call or an import another way.
func TestAUsageContractDetectsViolationsInRealSource(t *testing.T) {
	contract, err := usagecontract.Parse([]byte(`schema: codefly/usage/v1
summary: Reach the store only through its library.
rules:
  - id: STORE-CONN-001
    title: Open the store only through the library
    severity: error
    since: 1.4.0
    rationale: A raw connection bypasses the tenant checks.
    do: Request a scoped reader or writer from the library.
    detect:
      - language: go
        forbid-call: [database/sql.Open, github.com/jackc/pgx/v5/pgxpool.New]
        forbid-import: [github.com/lib/pq]
      - language: python
        forbid-call: [psycopg.connect]
        forbid-import: [asyncpg]
    except-paths: ["**/*_test.go"]
`))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for name, body := range map[string]string{
		"store/open.go": `package store

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context, dsn string) error {
	if _, err := sql.Open("pgx", dsn); err != nil {
		return err
	}
	_, err := pgxpool.New(ctx, dsn)
	return err
}
`,
		"store/driver.go": `package store

import _ "github.com/lib/pq"

func Registered() bool { return true }
`,
		"store/clean.go": `package store

import "database/sql"

func Missing(err error) bool { return err == sql.ErrNoRows }

func Named(name string) sql.NamedArg { return sql.Named(name, 1) }
`,
		"store/open_test.go": `package store

import (
	"database/sql"
	"testing"
)

func TestOpen(t *testing.T) {
	if _, err := sql.Open("pgx", "dsn"); err != nil {
		t.Fatal(err)
	}
}
`,
		"app/db.py": `import psycopg
from asyncpg import pool


def connect(dsn):
    return psycopg.connect(dsn)
`,
		// A call outside every function: a module's top-level statement and
		// a class body in Python, a package-level initializer in Go.
		"app/module_level.py": `import psycopg

connection = psycopg.connect("dsn")


class Holder:
    shared = psycopg.connect("dsn")

    def reconnect(self):
        return psycopg.connect("dsn")
`,
		"store/package_level.go": `package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

var pool, poolErr = pgxpool.New(context.Background(), "dsn")

var lazily = func() error {
	_, err := pgxpool.New(context.Background(), "dsn")
	return err
}
`,
		"app/clean.py": `import os


def connect(dsn):
    return os.environ.get(dsn)
`,
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	response, err := newSemanticServer(root).Execute(t.Context(), &codev0.CodeRequest{Operation: &codev0.CodeRequest_GetSemanticIndex{GetSemanticIndex: &codev0.GetSemanticIndexRequest{}}})
	if err != nil {
		t.Fatal(err)
	}
	index := response.GetGetSemanticIndex()
	if index == nil || index.GetState() != basev0.SemanticIndexState_SEMANTIC_INDEX_STATE_COMPLETE {
		t.Fatalf("semantic index = %v, failure = %v", index, response.GetFailure())
	}
	type found struct {
		rule, kind, symbol, path string
		line                     int32
	}
	var got []found
	for _, finding := range contract.Detect(index) {
		got = append(got, found{finding.RuleID, finding.Kind, finding.Symbol, finding.Path, finding.Line})
	}
	want := []found{
		{"STORE-CONN-001", usagecontract.FindingImport, "asyncpg", "app/db.py", 0},
		{"STORE-CONN-001", usagecontract.FindingCall, "psycopg.connect", "app/db.py", 6},
		{"STORE-CONN-001", usagecontract.FindingCall, "psycopg.connect", "app/module_level.py", 3},
		{"STORE-CONN-001", usagecontract.FindingCall, "psycopg.connect", "app/module_level.py", 7},
		{"STORE-CONN-001", usagecontract.FindingCall, "psycopg.connect", "app/module_level.py", 10},
		{"STORE-CONN-001", usagecontract.FindingImport, "github.com/lib/pq", "store/driver.go", 0},
		{"STORE-CONN-001", usagecontract.FindingCall, "database/sql.Open", "store/open.go", 11},
		{"STORE-CONN-001", usagecontract.FindingCall, "github.com/jackc/pgx/v5/pgxpool.New", "store/open.go", 14},
		{"STORE-CONN-001", usagecontract.FindingCall, "github.com/jackc/pgx/v5/pgxpool.New", "store/package_level.go", 9},
		{"STORE-CONN-001", usagecontract.FindingCall, "github.com/jackc/pgx/v5/pgxpool.New", "store/package_level.go", 12},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	// This analyzer reads every call site, and says so; an index that does
	// not say so is one a contract reports as partly unread.
	if !index.GetFileCallsProjected() {
		t.Fatal("the analyzer projects calls outside functions and does not say so")
	}
	if unread := contract.Unread(index); len(unread) != 0 {
		t.Fatalf("nothing is unread in this index: %v", unread)
	}
	// A call is projected once: on its function, or on its file.
	seen := map[string]int{}
	for _, file := range index.GetFiles() {
		for _, call := range file.GetCalls() {
			seen[call.GetLocation().String()+call.GetName()]++
		}
	}
	for _, symbol := range index.GetSymbols() {
		for _, call := range symbol.GetCalls() {
			seen[call.GetLocation().String()+call.GetName()]++
		}
	}
	for at, times := range seen {
		if times != 1 {
			t.Errorf("a call site is projected %d times: %s", times, at)
		}
	}
}
