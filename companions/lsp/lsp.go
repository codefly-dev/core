package lsp

import (
	"context"
	"fmt"

	runners "github.com/codefly-dev/core/runners/base"

	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/resources"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// LocationResult represents a source location returned by LSP operations.
type LocationResult struct {
	File      string // relative path within the workspace
	Line      int    // 1-based
	Column    int    // 1-based
	EndLine   int    // 1-based
	EndColumn int    // 1-based
}

// DiagnosticResult represents a diagnostic (error, warning, hint) from LSP.
type DiagnosticResult struct {
	File      string
	Line      int32
	Column    int32
	EndLine   int32
	EndColumn int32
	Message   string
	Severity  string // "error", "warning", "information", "hint"
	Source    string
	Code      string
}

// TextEditResult represents a text edit from an LSP rename or code action.
type TextEditResult struct {
	File        string
	StartLine   int32
	StartColumn int32
	EndLine     int32
	EndColumn   int32
	NewText     string
}

// HoverResult represents hover information at a position.
type HoverResult struct {
	Content  string // hover content (may be markdown)
	Language string // language hint for code blocks
}

// Client is a language-agnostic LSP client interface.
// Implementations talk to a language server (e.g. gopls, pylsp) running
// inside a companion environment (Docker, Nix, or local).
type Client interface {
	// ListSymbols returns symbols for a file (relative path) or the whole
	// workspace when file is empty.
	ListSymbols(ctx context.Context, file string) ([]*codev0.Symbol, error)

	// NotifyChange sends textDocument/didChange to the LSP server.
	// Used for incremental indexing when the AI agent edits a file.
	NotifyChange(ctx context.Context, file string, content string) error

	// NotifySave sends textDocument/didSave to the LSP server.
	NotifySave(ctx context.Context, file string) error

	// Diagnostics returns compiler/linter diagnostics for a file or the
	// entire workspace (if file is empty).
	Diagnostics(ctx context.Context, file string) ([]DiagnosticResult, error)

	// Definition returns the definition location(s) for the symbol at the
	// given 1-based line and column.
	Definition(ctx context.Context, file string, line, col int) ([]LocationResult, error)

	// References returns all usage locations of the symbol at the given
	// 1-based line and column.
	References(ctx context.Context, file string, line, col int) ([]LocationResult, error)

	// Rename performs a language-aware rename of the symbol at the given
	// position. Returns the text edits that were (or should be) applied.
	Rename(ctx context.Context, file string, line, col int, newName string) ([]TextEditResult, error)

	// Hover returns type information and documentation at the given position.
	Hover(ctx context.Context, file string, line, col int) (*HoverResult, error)

	// Close shuts down the LSP server and companion.
	// Safe to call multiple times.
	Close(ctx context.Context) error
}

// LanguageConfig holds per-language settings for the LSP companion.
type LanguageConfig struct {
	// CompanionImage returns the Docker image for this language companion.
	// May return nil if the language can run locally or via Nix.
	CompanionImage func(ctx context.Context) (*resources.DockerImage, error)

	// LSPBinary is the LSP server binary (e.g. "gopls").
	LSPBinary string

	// LSPListenArgs returns the arguments for the LSP binary given the
	// dynamically chosen port. Never hardcode a port.
	LSPListenArgs func(port int) []string

	// LanguageID is the LSP languageId string (e.g. "go", "python").
	LanguageID string

	// FileExtensions lists source file suffixes (e.g. [".go"], [".py"]).
	FileExtensions []string

	// SkipDirs are directory names to skip when walking the workspace.
	SkipDirs []string

	// SetupRunner is an optional hook to configure the companion runner
	// before Init (e.g. mount Go module cache). May be nil.
	// Uses the CompanionRunner interface -- works with any backend.
	SetupRunner func(ctx context.Context, runner runners.CompanionRunner, sourceDir string)
}

// registry maps languages to their LSP configs.
var registry = map[languages.Language]*LanguageConfig{}

// Register adds a language config to the registry.
func Register(lang languages.Language, cfg *LanguageConfig) {
	registry[lang] = cfg
}

// NewClient creates an LSP client for the given language and source directory.
// The source directory is the root where the language project lives (e.g.
// where go.mod or pyproject.toml is).
// Ports are picked dynamically. Backend is auto-detected.
// The caller must call Close() when done.
func NewClient(ctx context.Context, lang languages.Language, sourceDir string) (Client, error) {
	cfg, ok := registry[lang]
	if !ok {
		return nil, fmt.Errorf("no LSP config registered for language %s", lang)
	}
	return newCompanionClient(ctx, cfg, sourceDir)
}
