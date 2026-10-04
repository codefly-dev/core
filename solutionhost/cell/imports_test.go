package cell

import (
	"go/build"
	"testing"
)

// packageImports is this package's own non-test imports.
func packageImports(t *testing.T) []string {
	t.Helper()
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	return pkg.Imports
}
