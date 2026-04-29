// Package version exposes the current codefly build version and is the
// single source of truth used by both the CLI (--version) and the
// embedded agents.
//
// At release time the version string is overwritten via -ldflags; in
// dev builds it falls back to a compiled-in default.
package version
