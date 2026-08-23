// Package codeserver assembles the DefaultCodeServer that in-process consumers
// (the CLI, the gateway) run, deciding at build time whether to link the
// tree-sitter semantic analyzer.
//
// New is the single entry point, so consumers never reinvent the cgo/no-cgo
// split themselves. On a normal build it installs core/code/semantic — the
// tree-sitter CGO stack — so the server answers source-semantics operations.
// Built with -tags codefly_nosemantic it returns a plain DefaultCodeServer that
// never imports semantic, keeping the binary CGO-free (linkable with
// CGO_ENABLED=0 for alpine/static targets such as companion publish).
//
// codefly_nosemantic is the canonical, cross-repo build tag for this switch.
// See docs/cgo.md for the CGO surface contract and the CI guard that enforces
// it.
package codeserver
