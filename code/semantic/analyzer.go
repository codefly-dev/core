// Package semantic holds the tree-sitter source-analysis stack that the base
// code server deliberately omits. Importing it pulls the tree-sitter CGO
// grammars, so only tools that need source semantics depend on it; Go service
// agents keep core/code CGO-free and build with CGO_ENABLED=0.
package semantic

import "github.com/codefly-dev/core/code"

// Analyzer implements code.SemanticAnalyzer using tree-sitter grammars. Install
// it with code.WithSemanticAnalyzer(semantic.New()) or SetSemanticAnalyzer.
type Analyzer struct{}

// New returns a tree-sitter backed semantic analyzer.
func New() *Analyzer { return &Analyzer{} }

var _ code.SemanticAnalyzer = (*Analyzer)(nil)

// maxSourceFileSize bounds the bytes read for a single source file during
// inspection.
const maxSourceFileSize = 10 * 1024 * 1024 // 10MB

// Hash returns the lowercase SHA-256 hex digest used for semantic declaration
// identity.
func Hash(value []byte) string { return semanticHash(value) }
