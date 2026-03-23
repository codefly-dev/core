package lsp

import (
	"strings"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"github.com/codefly-dev/core/graph"
)

// Scope identifies the repo/module/service context for stable cross-repo node IDs.
type Scope struct {
	Repo    string
	Module  string
	Service string
}

// BuildGraphFromSymbols builds a generic graph from LSP symbols: file and symbol nodes,
// contains (file -> symbol), and child_of (symbol -> symbol) edges.
// Use the same Scope across repos/languages to get a unified cross-repo, cross-language graph.
func BuildGraphFromSymbols(symbols []*codev0.Symbol, file string, scope Scope) *graph.Graph {
	g := graph.New("lsp-" + file)
	fileID := graph.FileID(scope.Repo, scope.Module, scope.Service, file)
	g.AddNode(fileID, graph.KindFile)
	g.SetAttr(fileID, graph.KindFile, "path", file)

	for _, sym := range symbols {
		addSymbolToGraph(g, sym, file, fileID, scope)
	}
	return g
}

func addSymbolToGraph(g *graph.Graph, sym *codev0.Symbol, file, fileID string, scope Scope) {
	symID := graph.SymbolID(scope.Repo, scope.Module, scope.Service, file, sym.Name)
	g.AddNode(symID, graph.KindSymbol)
	g.SetAttr(symID, graph.KindSymbol, "name", sym.Name)
	g.SetAttr(symID, graph.KindSymbol, "kind", SymbolKindString(sym.Kind))
	if sym.Location != nil {
		g.SetAttr(symID, graph.KindSymbol, "line", sym.Location.Line)
		g.SetAttr(symID, graph.KindSymbol, "file", sym.Location.File)
	}
	if sym.Signature != "" {
		g.SetAttr(symID, graph.KindSymbol, "signature", sym.Signature)
	}
	g.AddEdge(fileID, symID, graph.EdgeContains)

	for _, child := range sym.Children {
		addSymbolToGraph(g, child, file, fileID, scope)
		g.AddEdge(graph.SymbolID(scope.Repo, scope.Module, scope.Service, file, child.Name), symID, graph.EdgeChildOf)
	}
}

// SymbolKindString returns the proto symbol kind as string for graph attrs.
func SymbolKindString(k codev0.SymbolKind) string {
	return strings.TrimPrefix(strings.ToLower(k.String()), "symbol_kind_")
}
