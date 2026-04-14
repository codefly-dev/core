package treesitter

// ARCHITECTURE: ParseBytes is the pure-parsing entry point. It takes file
// bytes in memory and returns the extracted symbols. NO filesystem IO.
// This is the API that callers like Mind use after reading files through
// their own gRPC/agent layer — Mind never touches the filesystem itself.

import (
	"context"
	"fmt"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"github.com/codefly-dev/core/languages"
)

// Parser is a lightweight, concurrency-safe wrapper that caches one
// *sitter.Parser per goroutine via a sync.Pool. Reuse cuts allocation
// overhead to near zero when parsing thousands of files in parallel.
type Parser struct {
	lang *languages.Language
	cfg  *LanguageConfig
	pool sync.Pool
}

// NewParser returns a Parser for a registered language.
// Returns an error if the language has not been imported (registered).
func NewParser(lang languages.Language) (*Parser, error) {
	cfg := Lookup(lang)
	if cfg == nil {
		return nil, fmt.Errorf("no tree-sitter grammar registered for %q (import the language subpackage)", lang)
	}
	p := &Parser{cfg: cfg}
	grammar := cfg.Grammar()
	if grammar == nil {
		return nil, fmt.Errorf("%s: Grammar() returned nil", cfg.LanguageID)
	}
	p.pool = sync.Pool{
		New: func() any {
			parser := sitter.NewParser()
			parser.SetLanguage(grammar)
			return parser
		},
	}
	return p, nil
}

// Config returns the language configuration the parser was built with.
// Useful to callers that want to inspect file extensions or skip dirs.
func (p *Parser) Config() *LanguageConfig {
	return p.cfg
}

// ParseBytes parses the given content and extracts symbols via the language's
// SymbolExtractor. relPath is used to populate Symbol.Location.File.
// Thread-safe: each call acquires a pooled *sitter.Parser.
func (p *Parser) ParseBytes(ctx context.Context, relPath string, content []byte) ([]*codev0.Symbol, error) {
	parser := p.pool.Get().(*sitter.Parser)
	defer p.pool.Put(parser)

	tree, err := parser.ParseCtx(ctx, nil, content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relPath, err)
	}
	defer tree.Close()

	return p.cfg.ExtractSymbols(tree, content, relPath), nil
}

// ParseBytesWithDiagnostics is like ParseBytes but also returns syntax-error
// diagnostics (ERROR / MISSING nodes). Single parse pass, no extra work.
func (p *Parser) ParseBytesWithDiagnostics(
	ctx context.Context, relPath string, content []byte,
) (syms []*codev0.Symbol, diags []DiagnosticResult, err error) {
	parser := p.pool.Get().(*sitter.Parser)
	defer p.pool.Put(parser)

	tree, err := parser.ParseCtx(ctx, nil, content)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", relPath, err)
	}
	defer tree.Close()

	syms = p.cfg.ExtractSymbols(tree, content, relPath)
	walkErrorNodes(tree.RootNode(), relPath, &diags)
	return syms, diags, nil
}

// ParseBytesAll returns BOTH the extracted symbols AND the parse tree in a
// single pass. The caller OWNS the returned tree and must call tree.Close().
// Use this when you need to walk the AST after symbol extraction (e.g.
// building call-graph edges) without re-parsing the file.
func (p *Parser) ParseBytesAll(
	ctx context.Context, relPath string, content []byte,
) (syms []*codev0.Symbol, tree *sitter.Tree, err error) {
	parser := p.pool.Get().(*sitter.Parser)
	defer p.pool.Put(parser)

	tree, err = parser.ParseCtx(ctx, nil, content)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", relPath, err)
	}
	syms = p.cfg.ExtractSymbols(tree, content, relPath)
	return syms, tree, nil
}
