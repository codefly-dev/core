package code

import (
	"os"
	"testing"
)

// TestMain disables Go workspace resolution for the entire `code`
// package's test run.
//
// Why: testdata/repos/* contains vendored third-party Go modules
// (fatih/color, gorilla/mux, etc.) used by analysis-quality tests.
// Each one has its own go.mod. If the parent codefly.dev/go.work file
// is in scope, Go's tooling sees these nested modules and fails
// during test setup with:
//
//	stat .../testdata/repos/<name>/code: directory not found
//
// (Go is looking for a `code/` subdir because the workspace lists
// other modules with that codefly-convention layout, and fatih_color
// doesn't have one.)
//
// Setting GOWORK=off here scopes the workaround to this package's
// tests only — production code paths and other packages' tests are
// unaffected.
func TestMain(m *testing.M) {
	if err := os.Setenv("GOWORK", "off"); err != nil {
		// Setenv only fails on POSIX with bad chars in the value;
		// "off" is fine. If we somehow can't, fail fast rather
		// than running tests with the wrong environment.
		panic("cannot set GOWORK=off for tests: " + err.Error())
	}
	os.Exit(m.Run())
}
