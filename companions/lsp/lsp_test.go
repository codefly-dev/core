package lsp

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"github.com/codefly-dev/core/companions/testutil"
	"github.com/codefly-dev/core/languages"
	"github.com/stretchr/testify/require"
)

// testdataSampleDir returns the absolute path to lsp/testdata/sample.
func testdataSampleDir(t *testing.T) string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(filename), "testdata", "sample")
	abs, err := filepath.Abs(dir)
	require.NoError(t, err)
	return abs
}

// TestLSPClientListSymbols verifies single-file document symbol listing
// through the language-agnostic Client interface.
func TestLSPClientListSymbols(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	testutil.RequireGoImage(t, ctx)

	rootDir := testdataSampleDir(t)

	client, err := NewClient(ctx, languages.GO, rootDir)
	require.NoError(t, err, "failed to start LSP client (build: ./companions/scripts/build_companions.sh)")
	defer client.Close(ctx)

	symbols, err := client.ListSymbols(ctx, "sample.go")
	require.NoError(t, err)
	require.NotEmpty(t, symbols)

	symbolNames := make(map[string]codev0.SymbolKind)
	for _, s := range symbols {
		symbolNames[s.Name] = s.Kind
	}

	require.Contains(t, symbolNames, "Greeter", "expected Greeter struct")
	require.Equal(t, codev0.SymbolKind_SYMBOL_KIND_STRUCT, symbolNames["Greeter"])

	require.Contains(t, symbolNames, "NewGreeter", "expected NewGreeter function")
	require.Equal(t, codev0.SymbolKind_SYMBOL_KIND_FUNCTION, symbolNames["NewGreeter"])

	require.Contains(t, symbolNames, "(*Greeter).Hello", "expected Hello method")
	require.Equal(t, codev0.SymbolKind_SYMBOL_KIND_METHOD, symbolNames["(*Greeter).Hello"])

	require.Contains(t, symbolNames, "Version", "expected Version constant")
	require.Equal(t, codev0.SymbolKind_SYMBOL_KIND_CONSTANT, symbolNames["Version"])

	for _, s := range symbols {
		require.NotNil(t, s.Location, "symbol %s should have location", s.Name)
		require.Equal(t, "sample.go", s.Location.File)
		require.Greater(t, s.Location.Line, int32(0), "line should be positive for %s", s.Name)
	}

	t.Logf("Found %d symbols in sample.go", len(symbols))
	for _, s := range symbols {
		t.Logf("  %s (%s) at line %d", s.Name, s.Kind, s.Location.Line)
	}
}

// TestLSPClientListSymbolsWorkspace exercises workspace-wide symbol listing.
func TestLSPClientListSymbolsWorkspace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	testutil.RequireGoImage(t, ctx)

	rootDir := testdataSampleDir(t)

	client, err := NewClient(ctx, languages.GO, rootDir)
	require.NoError(t, err, "failed to start LSP client (build: ./companions/scripts/build_companions.sh)")
	defer client.Close(ctx)

	allSymbols, err := client.ListSymbols(ctx, "")
	require.NoError(t, err)
	require.NotEmpty(t, allSymbols, "workspace should have at least one symbol")

	names := make(map[string]bool)
	for _, s := range allSymbols {
		names[s.Name] = true
	}
	require.True(t, names["Greeter"] || names["NewGreeter"] || names["Version"],
		"workspace symbols should include Greeter/NewGreeter/Version from sample.go")

	t.Logf("Workspace had %d symbols total", len(allSymbols))
}

// TestLSPClientIncrementalChange verifies incremental indexing: open a file,
// list symbols, send a didChange adding a new function, list symbols again,
// and verify the new function appears.
func TestLSPClientIncrementalChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	testutil.RequireGoImage(t, ctx)

	rootDir := testdataSampleDir(t)

	client, err := NewClient(ctx, languages.GO, rootDir)
	require.NoError(t, err, "failed to start LSP client")
	defer client.Close(ctx)

	// Step 1: List symbols before change.
	symbolsBefore, err := client.ListSymbols(ctx, "sample.go")
	require.NoError(t, err)

	namesBefore := make(map[string]bool)
	for _, s := range symbolsBefore {
		namesBefore[s.Name] = true
	}
	require.False(t, namesBefore["Goodbye"], "Goodbye should not exist before change")

	// Step 2: Send didChange with a new function added.
	updatedContent := `// Package sample provides a minimal Go file for LSP tests.
package sample

// Greeter is a struct used to verify LSP document symbols.
type Greeter struct {
	Name string
}

// NewGreeter returns a new Greeter.
func NewGreeter(name string) *Greeter {
	return &Greeter{Name: name}
}

// Hello returns a greeting message.
func (g *Greeter) Hello() string {
	return "Hello, " + g.Name
}

const Version = "1.0.0"

// Goodbye is a new function added via incremental change.
func Goodbye() string {
	return "Goodbye!"
}
`

	err = client.NotifyChange(ctx, "sample.go", updatedContent)
	require.NoError(t, err)

	// Step 3: Give gopls a moment to process the change, then list symbols again.
	// We retry a few times because gopls may need a moment to re-analyze.
	var symbolsAfter []*codev0.Symbol
	for i := 0; i < 10; i++ {
		symbolsAfter, err = client.ListSymbols(ctx, "sample.go")
		require.NoError(t, err)

		found := false
		for _, s := range symbolsAfter {
			if s.Name == "Goodbye" {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	namesAfter := make(map[string]bool)
	for _, s := range symbolsAfter {
		namesAfter[s.Name] = true
	}

	require.True(t, namesAfter["Goodbye"], "Goodbye should appear after incremental change")
	require.True(t, namesAfter["Greeter"], "original symbols should still be present")
	require.True(t, namesAfter["NewGreeter"], "original symbols should still be present")

	t.Logf("After incremental change: %d symbols", len(symbolsAfter))
	for _, s := range symbolsAfter {
		t.Logf("  %s (%s) at line %d", s.Name, s.Kind, s.Location.Line)
	}
}
