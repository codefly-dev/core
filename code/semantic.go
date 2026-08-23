package code

import (
	"context"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// SymbolSpan pairs a body-free semantic symbol with the exact parser-owned byte
// range of its declaration. It is the only tree-sitter-derived value the base
// server handles, so typed symbol mutation can live here while the parsing that
// produces it stays in core/code/semantic.
type SymbolSpan struct {
	Symbol         *basev0.SemanticSymbol
	StartByte      uint
	EndByte        uint
	DeclarationKey string
}

// SemanticAnalyzer supplies source-language parsing built on the tree-sitter
// CGO stack. The base server omits it so Go agents build with CGO_ENABLED=0;
// callers that need source semantics install one from core/code/semantic via
// WithSemanticAnalyzer or SetSemanticAnalyzer. Operations that require it fail
// with an unsupported-operation failure when none is installed.
type SemanticAnalyzer interface {
	// SemanticIndex scans sourceDir through fs and returns the full typed index.
	SemanticIndex(ctx context.Context, sourceDir string, fs VFS) (*basev0.SemanticIndex, error)
	// SourceImports returns a deterministic per-file import inventory for a
	// non-Go language rooted at root. The Go inventory is stdlib-based and lives
	// in core/code.
	SourceImports(ctx context.Context, fs VFS, root, language string) ([]*codev0.SourceFileInfo, error)
	// InspectProject fills declarative project metadata for the given
	// code-unit language (jvm, dotnet). Unsupported languages return an error.
	InspectProject(ctx context.Context, fs VFS, sourceDir, language string, info *codev0.GetProjectInfoResponse) error
	// DeclarationSpans returns every declaration span in body, keyed for typed
	// symbol mutation.
	DeclarationSpans(ctx context.Context, path string, body []byte) ([]SymbolSpan, error)
	// ValidateSyntax reports whether body parses as valid source for path.
	ValidateSyntax(ctx context.Context, path string, body []byte) error
	// Language returns the semantic language name for path and whether typed
	// symbol mutation supports it.
	Language(path string) (name string, supported bool)
}

// WithSemanticAnalyzer installs the source-language analyzer. Without it the
// server stays free of the tree-sitter CGO stack and semantic operations report
// unsupported.
//
// Consumers that need a build-tag-aware server — analyzer installed on normal
// builds, CGO-free under -tags codefly_nosemantic — should call
// code/codeserver.New instead of wiring this option (and the build-tag split)
// themselves. See docs/cgo.md.
func WithSemanticAnalyzer(analyzer SemanticAnalyzer) ServerOption {
	return func(s *DefaultCodeServer) { s.semantic = analyzer }
}

// SetSemanticAnalyzer installs or replaces the source-language analyzer after
// construction.
func (s *DefaultCodeServer) SetSemanticAnalyzer(analyzer SemanticAnalyzer) {
	s.semantic = analyzer
}
