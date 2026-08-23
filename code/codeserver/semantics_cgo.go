//go:build !codefly_nosemantic

package codeserver

import (
	"github.com/codefly-dev/core/code"
	"github.com/codefly-dev/core/code/semantic"
)

// New returns a DefaultCodeServer with the tree-sitter semantic analyzer
// installed. Importing core/code/semantic pulls the tree-sitter CGO stack, so
// this variant cannot link under CGO_ENABLED=0 — an accidental CGO-free build
// fails loudly at link time rather than silently dropping source semantics.
// Build with -tags codefly_nosemantic to select the CGO-free variant.
func New(root string, opts ...code.ServerOption) *code.DefaultCodeServer {
	return code.NewDefaultCodeServer(root, append(opts, code.WithSemanticAnalyzer(semantic.New()))...)
}
