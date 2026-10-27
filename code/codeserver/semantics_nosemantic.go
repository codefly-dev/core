//go:build codefly_nosemantic

package codeserver

import "github.com/codefly-dev/core/code"

// SemanticOptions returns no options: this variant never imports
// core/code/semantic, so the binary links with CGO_ENABLED=0. A server built
// from these options reports an unsupported-operation failure for
// source-semantics requests, which is the same answer New gives here.
func SemanticOptions() []code.ServerOption {
	return nil
}

// New returns a plain DefaultCodeServer with no semantic analyzer. Selected by
// the codefly_nosemantic build tag, this variant never imports
// core/code/semantic, so the binary links with CGO_ENABLED=0. Source-semantics
// operations report an unsupported-operation failure.
func New(root string, opts ...code.ServerOption) *code.DefaultCodeServer {
	return code.NewDefaultCodeServer(root, append(opts, SemanticOptions()...)...)
}
