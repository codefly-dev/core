//go:build !codefly_nosemantic

package codeserver

import (
	"github.com/codefly-dev/core/code"
	"github.com/codefly-dev/core/code/semantic"
)

// SemanticOptions returns the ServerOption values that install the tree-sitter
// semantic analyzer. It exists because a consumer that builds its own
// DefaultCodeServer needs the options, not a finished server: without it the
// only way to reach the analyzer was to import core/code/semantic directly,
// which is how an agent acquired an unconditional cgo edge that
// scripts/check_cgo_free.sh cannot see — it guards core's packages, and the
// import was one repo away. Take this instead of naming semantic.New yourself.
//
// Importing core/code/semantic pulls the tree-sitter CGO stack, so this variant
// cannot link under CGO_ENABLED=0. Build with -tags codefly_nosemantic to
// select the CGO-free variant, which returns no options.
func SemanticOptions() []code.ServerOption {
	return []code.ServerOption{code.WithSemanticAnalyzer(semantic.New())}
}

// New returns a DefaultCodeServer with the tree-sitter semantic analyzer
// installed. Importing core/code/semantic pulls the tree-sitter CGO stack, so
// this variant cannot link under CGO_ENABLED=0 — an accidental CGO-free build
// fails loudly at link time rather than silently dropping source semantics.
// Build with -tags codefly_nosemantic to select the CGO-free variant.
func New(root string, opts ...code.ServerOption) *code.DefaultCodeServer {
	return code.NewDefaultCodeServer(root, append(opts, SemanticOptions()...)...)
}
