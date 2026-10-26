package module

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheDocumentedFixtureCountIsTheKit's: docs/module-contract.md
// states how many documents this kit ships, and that number went stale the
// first time a rule was added with its fixture. A count in prose that nothing
// checks is a count that is wrong.
func TestTheDocumentedFixtureCountIsTheKits(t *testing.T) {
	all, err := Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "docs", "module-contract.md")
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stated := fmt.Sprintf("`modulecontract.Fixtures()` (%d", len(all))
	if !strings.Contains(string(document), stated) {
		t.Fatalf("%s does not state that modulecontract ships %d documents (looking for %q)", path, len(all), stated)
	}
}
