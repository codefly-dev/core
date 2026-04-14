package golang_test

// Real integration test for the Go tree-sitter client. Parses a fixture file
// from disk, asserts symbols, diagnostics, definition, and references.
// NO MOCKS: this runs the real parser on real source.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/codefly-dev/core/companions/treesitter"
	_ "github.com/codefly-dev/core/companions/treesitter/golang"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"github.com/codefly-dev/core/languages"
)

const fixture = `package sample

import "fmt"

// Greeter produces greetings.
type Greeter struct {
	Prefix string
	count  int
}

// Greet returns a greeting for name.
func (g *Greeter) Greet(name string) string {
	g.count++
	return fmt.Sprintf("%s, %s!", g.Prefix, name)
}

type Speaker interface {
	Speak(msg string) string
}

const Version = "1.0"

var Default = &Greeter{Prefix: "Hello"}

func NewGreeter(prefix string) *Greeter {
	return &Greeter{Prefix: prefix}
}
`

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir
}

func TestListSymbols_Go(t *testing.T) {
	ctx := context.Background()
	dir := writeFixture(t)

	client, err := treesitter.NewClient(ctx, languages.GO, dir)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close(ctx)

	syms, err := client.ListSymbols(ctx, "sample.go")
	if err != nil {
		t.Fatalf("ListSymbols: %v", err)
	}
	if len(syms) == 0 {
		t.Fatal("no symbols returned")
	}

	byName := map[string]*codev0.Symbol{}
	for _, s := range syms {
		byName[s.Name] = s
	}

	cases := []struct {
		name string
		kind codev0.SymbolKind
	}{
		{"Greeter", codev0.SymbolKind_SYMBOL_KIND_STRUCT},
		{"Greet", codev0.SymbolKind_SYMBOL_KIND_METHOD},
		{"Speaker", codev0.SymbolKind_SYMBOL_KIND_INTERFACE},
		{"Version", codev0.SymbolKind_SYMBOL_KIND_CONSTANT},
		{"Default", codev0.SymbolKind_SYMBOL_KIND_VARIABLE},
		{"NewGreeter", codev0.SymbolKind_SYMBOL_KIND_FUNCTION},
	}
	for _, c := range cases {
		s, ok := byName[c.name]
		if !ok {
			names := make([]string, 0, len(byName))
			for n := range byName {
				names = append(names, n)
			}
			sort.Strings(names)
			t.Fatalf("missing symbol %q; got %v", c.name, names)
		}
		if s.Kind != c.kind {
			t.Errorf("%s: kind = %v, want %v", c.name, s.Kind, c.kind)
		}
	}

	greet := byName["Greet"]
	if greet.Parent != "Greeter" {
		t.Errorf("Greet.Parent = %q, want Greeter", greet.Parent)
	}
	if greet.QualifiedName != "sample.Greeter.Greet" {
		t.Errorf("Greet.QualifiedName = %q, want sample.Greeter.Greet", greet.QualifiedName)
	}
	if greet.Signature == "" {
		t.Error("Greet.Signature is empty")
	}

	gr := byName["Greeter"]
	if len(gr.Children) != 2 {
		t.Errorf("Greeter.Children = %d, want 2 (Prefix, count)", len(gr.Children))
	}
}

func TestDiagnostics_SyntaxError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	broken := "package sample\n\nfunc Broken() {\n  x := \n}\n"
	path := filepath.Join(dir, "broken.go")
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	client, err := treesitter.NewClient(ctx, languages.GO, dir)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close(ctx)

	diags, err := client.Diagnostics(ctx, "broken.go")
	if err != nil {
		t.Fatalf("Diagnostics: %v", err)
	}
	if len(diags) == 0 {
		t.Fatal("expected syntax error diagnostic, got none")
	}
	for _, d := range diags {
		if d.Severity != "error" {
			t.Errorf("severity = %q, want error", d.Severity)
		}
		if d.Source != "tree-sitter" {
			t.Errorf("source = %q, want tree-sitter", d.Source)
		}
	}
}

func TestDefinitionAndReferences(t *testing.T) {
	ctx := context.Background()
	dir := writeFixture(t)

	client, err := treesitter.NewClient(ctx, languages.GO, dir)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close(ctx)

	// `NewGreeter` is defined on line 26 (1-based) starting at column 6.
	// Find it programmatically so the test stays robust to fixture edits.
	syms, err := client.ListSymbols(ctx, "sample.go")
	if err != nil {
		t.Fatalf("ListSymbols: %v", err)
	}
	var newGreeter *codev0.Symbol
	for _, s := range syms {
		if s.Name == "NewGreeter" {
			newGreeter = s
			break
		}
	}
	if newGreeter == nil {
		t.Fatal("NewGreeter not found")
	}

	line := int(newGreeter.Location.Line)
	col := int(newGreeter.Location.Column)
	// The Location covers the func keyword; bump forward to land on the identifier.
	col += len("func ")

	defs, err := client.Definition(ctx, "sample.go", line, col)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("Definition returned no results for NewGreeter")
	}
	got := defs[0]
	if got.File != "sample.go" {
		t.Errorf("Definition.File = %q, want sample.go", got.File)
	}
	if got.Confidence < 1.0 {
		t.Errorf("Definition.Confidence = %v, want 1.0 (local scope)", got.Confidence)
	}

	refs, err := client.References(ctx, "sample.go", line, col)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	// At least the definition itself.
	if len(refs) == 0 {
		t.Fatal("References returned no results")
	}
}
