package dap

import (
	"context"
	"fmt"

	runners "github.com/codefly-dev/core/runners/base"

	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/resources"
)

// BreakpointResult represents a verified breakpoint.
type BreakpointResult struct {
	ID       int
	Verified bool
	File     string
	Line     int
	Message  string
}

// StackFrame represents a frame in a call stack.
type StackFrame struct {
	ID     int
	Name   string
	File   string
	Line   int
	Column int
}

// Variable represents a variable or expression result.
type Variable struct {
	Name  string
	Value string
	Type  string
	Ref   int // variablesReference for structured types (0 = leaf)
}

// ThreadInfo represents a thread in the debuggee.
type ThreadInfo struct {
	ID   int
	Name string
}

// Scope represents a variable scope within a stack frame.
type Scope struct {
	Name string
	Ref  int // variablesReference to get the scope's variables
}

// StoppedEvent is emitted when the debuggee stops.
type StoppedEvent struct {
	ThreadID int
	Reason   string // "breakpoint", "step", "exception", "pause"
}

// OutputEvent is emitted when the debuggee produces output.
type OutputEvent struct {
	Category string // "console", "stdout", "stderr"
	Output   string
}

// Client is a language-agnostic DAP client interface.
// Implementations talk to a debug adapter (e.g. dlv dap, debugpy)
// running inside a companion environment (Docker, Nix, or local).
type Client interface {
	// Launch starts a new debug target process.
	Launch(ctx context.Context, program string, args []string, env map[string]string) error

	// Attach connects to an already-running process.
	Attach(ctx context.Context, pid int) error

	// SetBreakpoints sets breakpoints for a file, replacing any previous ones.
	SetBreakpoints(ctx context.Context, file string, lines []int) ([]BreakpointResult, error)

	// SetFunctionBreakpoints sets breakpoints by function name.
	SetFunctionBreakpoints(ctx context.Context, names []string) ([]BreakpointResult, error)

	// Continue resumes execution until next breakpoint or termination.
	Continue(ctx context.Context, threadID int) error

	// Next steps over one source line.
	Next(ctx context.Context, threadID int) error

	// StepIn steps into a function call.
	StepIn(ctx context.Context, threadID int) error

	// StepOut steps out of the current function.
	StepOut(ctx context.Context, threadID int) error

	// Pause suspends execution of a thread.
	Pause(ctx context.Context, threadID int) error

	// Threads returns all threads in the debuggee.
	Threads(ctx context.Context) ([]ThreadInfo, error)

	// StackTrace returns the call stack for a thread.
	StackTrace(ctx context.Context, threadID int) ([]StackFrame, error)

	// Scopes returns the variable scopes for a stack frame.
	Scopes(ctx context.Context, frameID int) ([]Scope, error)

	// Variables returns child variables for a variablesReference.
	Variables(ctx context.Context, ref int) ([]Variable, error)

	// Evaluate evaluates an expression in the context of a frame.
	Evaluate(ctx context.Context, frameID int, expression string) (*Variable, error)

	// OnStopped registers a callback for stopped events.
	OnStopped(handler func(StoppedEvent))

	// OnOutput registers a callback for output events.
	OnOutput(handler func(OutputEvent))

	// OnTerminated registers a callback for when the debuggee exits.
	OnTerminated(handler func())

	// Close terminates the debug session and shuts down the adapter.
	Close(ctx context.Context) error
}

// LanguageConfig holds per-language settings for the DAP companion.
type LanguageConfig struct {
	// CompanionImage returns the Docker image for this language companion.
	// May return nil if the language can run locally or via Nix.
	CompanionImage func(ctx context.Context) (*resources.DockerImage, error)

	// DAPBinary is the debug adapter binary (e.g. "dlv").
	DAPBinary string

	// DAPListenArgs returns the arguments to start the DAP server on a given port.
	// Never hardcode a port.
	DAPListenArgs func(port int) []string

	// LanguageID is the language identifier (e.g. "go", "python").
	LanguageID string

	// SetupRunner is an optional hook to configure the companion runner
	// before Init (e.g. mount caches). May be nil.
	// Uses the CompanionRunner interface -- works with any backend.
	SetupRunner func(ctx context.Context, runner runners.CompanionRunner, sourceDir string)
}

// registry maps languages to their DAP configs.
var registry = map[languages.Language]*LanguageConfig{}

// Register adds a language config to the DAP registry.
func Register(lang languages.Language, cfg *LanguageConfig) {
	registry[lang] = cfg
}

// NewClient creates a DAP client for the given language and source directory.
// The source directory is the root where the language project lives (e.g.
// where go.mod or pyproject.toml is).
// Ports are picked dynamically. Backend is auto-detected.
// The caller must call Close() when done.
func NewClient(ctx context.Context, lang languages.Language, sourceDir string) (Client, error) {
	cfg, ok := registry[lang]
	if !ok {
		return nil, fmt.Errorf("no DAP config registered for language %s", lang)
	}
	return newCompanionClient(ctx, cfg, sourceDir)
}
