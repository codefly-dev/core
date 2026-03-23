package lsp

import (
	"testing"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"github.com/codefly-dev/core/graph"
)

func TestBuildGraphFromSymbols_Empty(t *testing.T) {
	scope := Scope{Repo: "r", Module: "m", Service: "s"}
	g := BuildGraphFromSymbols(nil, "pkg/file.go", scope)
	if g == nil {
		t.Fatal("graph is nil")
	}
	nodes := g.Nodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 file node, got %d", len(nodes))
	}
	n := nodes[0]
	if n.Kind != graph.KindFile {
		t.Errorf("node kind: got %q", n.Kind)
	}
	fileID := graph.FileID("r", "m", "s", "pkg/file.go")
	if n.ID != fileID {
		t.Errorf("file node ID: got %q", n.ID)
	}
	if len(g.Edges()) != 0 {
		t.Fatalf("expected 0 edges, got %d", len(g.Edges()))
	}
}

func TestBuildGraphFromSymbols_FlatSymbols(t *testing.T) {
	scope := Scope{Repo: "r", Module: "m", Service: "s"}
	symbols := []*codev0.Symbol{
		{Name: "Foo", Kind: codev0.SymbolKind_SYMBOL_KIND_FUNCTION, Location: &codev0.Location{File: "pkg/h.go", Line: 10}},
		{Name: "Bar", Kind: codev0.SymbolKind_SYMBOL_KIND_STRUCT, Location: &codev0.Location{File: "pkg/h.go", Line: 20}},
	}
	g := BuildGraphFromSymbols(symbols, "pkg/h.go", scope)
	fileID := graph.FileID("r", "m", "s", "pkg/h.go")
	if !g.HasNode(fileID) {
		t.Fatal("file node missing")
	}
	symFoo := graph.SymbolID("r", "m", "s", "pkg/h.go", "Foo")
	symBar := graph.SymbolID("r", "m", "s", "pkg/h.go", "Bar")
	if !g.HasNode(symFoo) || !g.HasNode(symBar) {
		t.Fatalf("symbol nodes missing: hasFoo=%v hasBar=%v", g.HasNode(symFoo), g.HasNode(symBar))
	}
	edges := g.Edges()
	containsCount := 0
	for _, e := range edges {
		if e.Kind == graph.EdgeContains && e.From == fileID {
			containsCount++
		}
	}
	if containsCount != 2 {
		t.Fatalf("expected 2 contains edges from file, got %d", containsCount)
	}
	n := g.Node(symFoo)
	if n.Attrs["name"] != "Foo" || n.Attrs["kind"] != "function" {
		t.Errorf("Foo attrs: %+v", n.Attrs)
	}
	if n.Attrs["line"] != int32(10) {
		t.Errorf("Foo line: %v", n.Attrs["line"])
	}
}

func TestBuildGraphFromSymbols_NestedSymbols(t *testing.T) {
	scope := Scope{Module: "m", Service: "s"}
	child := &codev0.Symbol{Name: "Do", Kind: codev0.SymbolKind_SYMBOL_KIND_METHOD}
	parent := &codev0.Symbol{
		Name:     "Handler",
		Kind:     codev0.SymbolKind_SYMBOL_KIND_STRUCT,
		Children: []*codev0.Symbol{child},
	}
	g := BuildGraphFromSymbols([]*codev0.Symbol{parent}, "pkg/handlers.go", scope)
	parentID := graph.SymbolID("", "m", "s", "pkg/handlers.go", "Handler")
	childID := graph.SymbolID("", "m", "s", "pkg/handlers.go", "Do")
	if !g.HasNode(parentID) || !g.HasNode(childID) {
		t.Fatalf("parent or child node missing")
	}
	childOfCount := 0
	for _, e := range g.Edges() {
		if e.Kind == graph.EdgeChildOf && e.From == childID && e.To == parentID {
			childOfCount++
		}
	}
	if childOfCount != 1 {
		t.Fatalf("expected 1 child_of edge from Do to Handler, got %d", childOfCount)
	}
}

func TestSymbolKindString(t *testing.T) {
	if SymbolKindString(codev0.SymbolKind_SYMBOL_KIND_FUNCTION) != "function" {
		t.Errorf("function kind")
	}
	if SymbolKindString(codev0.SymbolKind_SYMBOL_KIND_STRUCT) != "struct" {
		t.Errorf("struct kind")
	}
}