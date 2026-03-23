package code

import (
	"context"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// SymbolProvider abstracts how symbols are resolved for a codebase.
// Default implementation: ASTSymbolProvider (Go AST via ParseGoTree).
// Override with LSPSymbolProvider (gopls) for full cross-module resolution.
type SymbolProvider interface {
	ListSymbols(ctx context.Context, file string) ([]*codev0.Symbol, error)
}

// ASTSymbolProvider uses ParseGoTreeVFS to extract symbols from Go AST.
// Zero external dependencies, millisecond performance, works anywhere Go is installed.
// Tradeoff: no cross-module type resolution.
type ASTSymbolProvider struct {
	dir   string
	vfs   VFS
	graph *CodeGraph
}

// NewASTSymbolProvider creates a provider that lazily parses the Go source tree.
// Uses LocalVFS by default; callers can override vfs for non-local filesystems.
func NewASTSymbolProvider(dir string) *ASTSymbolProvider {
	return &ASTSymbolProvider{dir: dir, vfs: LocalVFS{}}
}

// NewASTSymbolProviderVFS creates a provider backed by the given VFS.
func NewASTSymbolProviderVFS(dir string, vfs VFS) *ASTSymbolProvider {
	return &ASTSymbolProvider{dir: dir, vfs: vfs}
}

func (p *ASTSymbolProvider) ensureGraph() error {
	if p.graph != nil {
		return nil
	}
	g, err := ParseGoTreeVFS(p.vfs, p.dir)
	if err != nil {
		return err
	}
	p.graph = g
	return nil
}

// ListSymbols returns proto Symbol messages for a specific file or all files
// (if file is empty).
func (p *ASTSymbolProvider) ListSymbols(_ context.Context, file string) ([]*codev0.Symbol, error) {
	if err := p.ensureGraph(); err != nil {
		return nil, err
	}

	var symbols []*codev0.Symbol
	for _, node := range p.graph.Nodes {
		if node.Kind == NodeFile || node.Kind == NodePackage {
			continue
		}
		if file != "" && node.File != file {
			continue
		}
		symbols = append(symbols, nodeToSymbol(node))
	}
	return symbols, nil
}

// Graph returns the underlying CodeGraph for callers that need it.
func (p *ASTSymbolProvider) Graph() (*CodeGraph, error) {
	if err := p.ensureGraph(); err != nil {
		return nil, err
	}
	return p.graph, nil
}

func nodeToSymbol(n *CodeNode) *codev0.Symbol {
	return &codev0.Symbol{
		Name:          n.Name,
		Kind:          nodeKindToProto(n.Kind),
		Signature:     n.Signature,
		Documentation: n.Doc,
		Parent:        parentFromID(n.ID),
		Location: &codev0.Location{
			File:    n.File,
			Line:    int32(n.Line),
			EndLine: int32(n.EndLine),
		},
	}
}

func nodeKindToProto(k NodeKind) codev0.SymbolKind {
	switch k {
	case NodeFunction:
		return codev0.SymbolKind_SYMBOL_KIND_FUNCTION
	case NodeMethod:
		return codev0.SymbolKind_SYMBOL_KIND_METHOD
	case NodeType:
		return codev0.SymbolKind_SYMBOL_KIND_STRUCT
	default:
		return codev0.SymbolKind_SYMBOL_KIND_UNKNOWN
	}
}

// parentFromID extracts the parent symbol name from a node ID like "file.go::Type.Method".
func parentFromID(id string) string {
	parts := splitNodeID(id)
	if len(parts) < 2 {
		return ""
	}
	name := parts[1]
	if dot := dotIndex(name); dot > 0 {
		return name[:dot]
	}
	return ""
}

func splitNodeID(id string) []string {
	idx := 0
	for i := 0; i < len(id)-1; i++ {
		if id[i] == ':' && id[i+1] == ':' {
			idx = i
			break
		}
	}
	if idx == 0 {
		return []string{id}
	}
	return []string{id[:idx], id[idx+2:]}
}

func dotIndex(s string) int {
	for i, c := range s {
		if c == '.' {
			return i
		}
	}
	return -1
}
