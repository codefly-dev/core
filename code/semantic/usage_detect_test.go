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
		{"STORE-CONN-001", usagecontract.FindingImport, "github.com/lib/pq", "store/driver.go", 0},
		{"STORE-CONN-001", usagecontract.FindingCall, "database/sql.Open", "store/open.go", 11},
		{"STORE-CONN-001", usagecontract.FindingCall, "github.com/jackc/pgx/v5/pgxpool.New", "store/open.go", 14},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}
