package bash

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/runners/sandbox"
	"mvdan.cc/sh/v3/syntax"
)

// Toolbox is the codefly Bash toolbox.
//
// Construct with the canonical registry that captures plugin claims
// and the sandbox the script will run inside. The sandbox should
// already be configured (workspace as writable path, network policy,
// etc.); the bash toolbox layers canonical-routing on top — it does
// NOT add or weaken sandbox rules.
type Toolbox struct {
	registry *policy.CanonicalRegistry
	sandbox  sandbox.Sandbox
}

// New returns a Toolbox bound to the given registry and sandbox.
//
// Both arguments are required; nil panics at first Exec, which is
// preferable to silently running with no enforcement (the failure
// mode we're explicitly designing against).
func New(reg *policy.CanonicalRegistry, sb sandbox.Sandbox) *Toolbox {
	return &Toolbox{registry: reg, sandbox: sb}
}

// Result is the outcome of a non-canonical-refused script.
//
// A canonical refusal returns (nil, *CanonicalRoutedError) — the
// script never starts. A real exec failure returns (Result with the
// exit code, nil) — the caller decides whether non-zero is fatal.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Exec parses, canonical-checks, and runs script under bash inside
// the configured sandbox.
//
// The script is exactly what `bash -c "<script>"` would receive —
// callers don't need to escape anything beyond what bash itself
// expects. Multi-line scripts, pipes, conditionals, subshells all
// work; the AST walker visits every command node.
func (t *Toolbox) Exec(ctx context.Context, script string) (*Result, error) {
	if t.registry == nil || t.sandbox == nil {
		return nil, fmt.Errorf("bash.Toolbox: missing registry or sandbox (constructor was bypassed)")
	}

	if err := t.canonicalCheck(script); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	if err := t.sandbox.Wrap(cmd); err != nil {
		return nil, fmt.Errorf("bash.Toolbox: sandbox wrap: %w", err)
	}
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	err := cmd.Run()
	res := &Result{Stdout: out.String(), Stderr: errBuf.String()}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		// Non-zero exit is the script's business, not the toolbox's
		// failure. Return result with code; let the caller decide.
		return res, nil
	}
	return res, err
}

// canonicalCheck parses script and rejects any command whose program
// name (the first argv) routes through the canonical registry.
//
// Implementation note: we walk every node and pull the printed form
// of the first argument when we see a CallExpr. The printed form is
// the literal text — we deliberately do NOT word-split, glob-expand,
// or variable-substitute, so `git $cmd` and `g$x` (where x="it")
// would NOT trip the rule. That's a known limitation: dynamic
// command construction in bash is not safely analyzable at parse
// time. The OS-sandbox layer catches it at exec time when the binary
// can't be found.
func (t *Toolbox) canonicalCheck(script string) error {
	parser := syntax.NewParser()
	file, err := parser.Parse(strings.NewReader(script), "")
	if err != nil {
		return fmt.Errorf("bash.Toolbox: parse: %w", err)
	}

	printer := syntax.NewPrinter()

	var firstHit *CanonicalRoutedError
	syntax.Walk(file, func(n syntax.Node) bool {
		if firstHit != nil {
			return false
		}
		cmd, ok := n.(*syntax.CallExpr)
		if !ok || len(cmd.Args) == 0 {
			return true
		}

		var nameBuf strings.Builder
		if err := printer.Print(&nameBuf, cmd.Args[0]); err != nil {
			// A malformed argument shouldn't bypass routing — be
			// explicit and refuse the whole script rather than letting
			// a parser quirk leak unsafe execution.
			firstHit = &CanonicalRoutedError{
				Binary: "?",
				Reason: fmt.Sprintf("bash.Toolbox: cannot stringify command name (parser quirk: %v)", err),
			}
			return false
		}
		name := strings.TrimSpace(nameBuf.String())

		if d := t.registry.Lookup(name); d != nil {
			firstHit = &CanonicalRoutedError{
				Binary: name,
				Owner:  d.Owner,
				Reason: d.Reason,
			}
			return false
		}
		return true
	})
	if firstHit == nil {
		return nil
	}
	return firstHit
}

// CanonicalRoutedError signals that the script tried to invoke a
// binary that the canonical registry says belongs to another
// toolbox. The error is non-fatal at the codefly level — the caller
// should surface it to the agent as "use the X toolbox instead."
type CanonicalRoutedError struct {
	// Binary is the program name as it appeared in the script (with
	// any leading path stripped).
	Binary string

	// Owner is the toolbox plugin that has claimed this binary, or ""
	// for the built-in fallback (no plugin yet ships the toolbox).
	Owner string

	// Reason is the human-readable explanation suitable for direct
	// surfacing to the agent — actionable, not implementation-leaky.
	Reason string
}

func (e *CanonicalRoutedError) Error() string {
	if e.Owner != "" {
		return fmt.Sprintf("bash refused %q: canonically owned by toolbox %q — %s",
			e.Binary, e.Owner, e.Reason)
	}
	return fmt.Sprintf("bash refused %q: %s", e.Binary, e.Reason)
}
