// Package codeserver assembles the DefaultCodeServer that in-process consumers
// (the CLI, the gateway) run, deciding at build time whether to link the
// tree-sitter semantic analyzer.
//
// New and SemanticOptions are the only entry points, so consumers never
// reinvent the cgo/no-cgo split themselves: New when codeserver builds the
// server, SemanticOptions when the consumer builds it and needs the analyzer
// option alone. Naming core/code/semantic directly instead pins a cgo edge the
// tag cannot remove, one repo away from the guard that would have caught it.
// On a normal build it installs core/code/semantic — the
// tree-sitter CGO stack — so the server answers source-semantics operations.
// Built with -tags codefly_nosemantic it returns a plain DefaultCodeServer that
// never imports semantic, keeping the binary CGO-free (linkable with
// CGO_ENABLED=0 for alpine/static targets such as companion publish).
//
// codefly_nosemantic is the canonical, cross-repo build tag for this switch.
// See docs/cgo.md for the CGO surface contract and the CI guard that enforces
// it.
package codeserver
