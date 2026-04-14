// Package treesitter implements code analysis using tree-sitter parsers.
//
// ARCHITECTURE: This package is THE backend for code intelligence. LSP was
// deleted — tree-sitter is the only parser.
//
// CIS Design Document v2.0:
//
//	"No LSP as primary layer. LSP is too stateful, too slow, and too single-client
//	 to serve concurrent sessions. The compiler runs once at commit time for type
//	 validation. Everything else is tree-sitter and graph."
//
// What tree-sitter gives us:
//   - Single-file, error-tolerant parsing (tolerates syntax errors)
//   - Incremental parsing (re-parse only changed bytes)
//   - In-process (no IPC, no language server lifecycle)
//   - Deterministic (same input → same tree)
//   - Multi-language (Go, Python, TypeScript, Rust, ...)
//
// What tree-sitter does NOT give us (and we layer on top):
//   - Type information → compiler at commit time
//   - Cross-file resolution → symbol index built over tree-sitter output
//   - Refactoring → agents edit via SymbolPatch
//
// Language support is added by creating a subpackage (e.g. treesitter/golang)
// that calls Register() in its init() with a *LanguageConfig containing the
// grammar and a SymbolExtractor. The core package has no per-language code.
package treesitter

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/codefly-dev/core/languages"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// LocationResult is a source location returned by tree-sitter resolution.
type LocationResult struct {
	File       string  // relative path within the workspace
	Line       int     // 1-based
	Column     int     // 1-based
	EndLine    int     // 1-based
	EndColumn  int     // 1-based
	Confidence float32 // 0.0–1.0 per CIS confidence model
	Source     string  // "tree_sitter" | "resolved_explicit" | "resolved_wildcard"
}

// DiagnosticResult is a SYNTAX diagnostic from tree-sitter (ERROR nodes).
// Type/semantic diagnostics come from the compiler at commit time, not here.
type DiagnosticResult struct {
	File      string
	Line      int32
	Column    int32
	EndLine   int32
	EndColumn int32
	Message   string
	Severity  string // always "error" — tree-sitter only emits ERROR nodes
	Source    string // "tree-sitter"
}

// HoverResult carries the text snippet under the cursor. Tree-sitter alone
// cannot produce semantic hover (types, docs). Callers that need rich hover
// layer the symbol index + summary store above this client.
type HoverResult struct {
	Content  string
	Language string
}

// Client is the language-agnostic tree-sitter analysis interface.
type Client interface {
	// ListSymbols returns symbols defined in a file (or the whole workspace
	// when file is empty).
	ListSymbols(ctx context.Context, file string) ([]*codev0.Symbol, error)

	// NotifyChange is a no-op for tree-sitter: the parser is stateless and
	// re-parses on demand.
	NotifyChange(ctx context.Context, file string, content string) error

	// NotifySave is a no-op for tree-sitter.
	NotifySave(ctx context.Context, file string) error

	// Diagnostics returns SYNTAX errors (ERROR nodes) for a file or the
	// entire workspace (file == "").
	Diagnostics(ctx context.Context, file string) ([]DiagnosticResult, error)

	// Definition returns the definition location for the identifier at the
	// given 1-based (line, col). Uses CIS 7.1 3-step resolution.
	Definition(ctx context.Context, file string, line, col int) ([]LocationResult, error)

	// References returns all usage locations of the identifier at (line, col).
	References(ctx context.Context, file string, line, col int) ([]LocationResult, error)

	// Hover returns the node text at (line, col).
	Hover(ctx context.Context, file string, line, col int) (*HoverResult, error)

	// Close releases parser resources. Safe to call multiple times.
	Close(ctx context.Context) error
}

// SymbolExtractor converts a parsed tree-sitter tree for one file into
// language-neutral Symbols. Implemented per language in the subpackage.
type SymbolExtractor func(tree *sitter.Tree, content []byte, relPath string) []*codev0.Symbol

// LanguageConfig holds per-language settings for the tree-sitter client.
type LanguageConfig struct {
	// LanguageID matches the LSP languageId convention ("go", "python").
	LanguageID string

	// FileExtensions lists source file suffixes (e.g. [".go"], [".py"]).
	FileExtensions []string

	// SkipDirs are directory names to skip when walking the workspace.
	SkipDirs []string

	// SkipSuffixes are file suffixes to skip (e.g. "_test.go").
	SkipSuffixes []string

	// Grammar returns the tree-sitter language grammar. Called once per client.
	Grammar func() *sitter.Language

	// ExtractSymbols turns a parsed tree into Symbols. Required.
	ExtractSymbols SymbolExtractor
}

// registry maps languages to their tree-sitter configs.
// Populated by language subpackages via Register().
var registry = map[languages.Language]*LanguageConfig{}

// Register adds a language config to the registry.
// Typically called from a language subpackage's init().
func Register(lang languages.Language, cfg *LanguageConfig) {
	if cfg == nil {
		panic("treesitter.Register: nil config")
	}
	if cfg.Grammar == nil {
		panic(fmt.Sprintf("treesitter.Register(%s): nil Grammar", lang))
	}
	if cfg.ExtractSymbols == nil {
		panic(fmt.Sprintf("treesitter.Register(%s): nil ExtractSymbols", lang))
	}
	registry[lang] = cfg
}

// Lookup returns the registered config for a language, or nil if missing.
// Exposed for tests and debugging.
func Lookup(lang languages.Language) *LanguageConfig {
	return registry[lang]
}

// NewClient creates a tree-sitter client for the given language and source dir.
// Unlike LSP NewClient, this does NOT spawn a subprocess — tree-sitter runs
// in-process. Construction is cheap so callers can create per-request if needed.
func NewClient(ctx context.Context, lang languages.Language, sourceDir string) (Client, error) {
	cfg, ok := registry[lang]
	if !ok {
		return nil, fmt.Errorf("no tree-sitter config registered for language %s — import the language subpackage (e.g. _ \"github.com/codefly-dev/core/companions/treesitter/golang\")", lang)
	}
	return newFileScopedClient(ctx, cfg, sourceDir)
}
