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
		name = strings.TrimPrefix(name, "\\") // `\git` is still git
		prog := basename(name)

		if d := t.registry.Lookup(name); d != nil {
			firstHit = &CanonicalRoutedError{Binary: name, Owner: d.Owner, Reason: d.Reason}
			return false
		}

		// Wrapper commands run ANOTHER program; checking only argv[0] let
		// `bash -c "git ..."`, `env git`, `sudo git`, `xargs git` etc. bypass
		// routing entirely.
		//
		// Shell `-c <script>` wrappers: recurse into the embedded script so
		// every command in it is checked, not just the first.
		switch prog {
		case "sh", "bash", "zsh", "dash", "ash", "ksh":
			if script, ok := shellDashCScript(cmd); ok {
				if err := t.canonicalCheck(script); err != nil {
					var routed *CanonicalRoutedError
					if errors.As(err, &routed) {
						firstHit = routed
						return false
					}
				}
			}
		default:
			// Prefix wrappers (env/sudo/xargs/...): resolve the wrapped
			// command name and look it up.
			if wrapped := wrappedCommandName(cmd, prog); wrapped != "" {
				if d := t.registry.Lookup(wrapped); d != nil {
					firstHit = &CanonicalRoutedError{Binary: wrapped, Owner: d.Owner, Reason: d.Reason}
					return false
				}
			}
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

// basename strips any leading path from a program name (`/usr/bin/git` → `git`).
func basename(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// wordLiteral returns the static string value of a shell word when it is fully
// static (a bare literal or a single/double-quoted literal with no expansions);
// ok is false if the word contains variable/command expansion, which we cannot
// safely resolve at parse time.
func wordLiteral(w *syntax.Word) (string, bool) {
	if w == nil {
		return "", false
	}
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, dp := range p.Parts {
				lit, ok := dp.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

// shellDashCScript returns the script passed to a shell via `-c` (e.g. the
// `git push` in `bash -c "git push"`), or ok=false if there is none.
func shellDashCScript(cmd *syntax.CallExpr) (string, bool) {
	for i := 1; i < len(cmd.Args); i++ {
		lit, ok := wordLiteral(cmd.Args[i])
		if !ok {
			continue
		}
		if lit == "-c" && i+1 < len(cmd.Args) {
			return wordLiteral(cmd.Args[i+1])
		}
	}
	return "", false
}

// wrappedCommandName returns the program name that a prefix wrapper (env, sudo,
// xargs, nohup, timeout, ...) will execute, or "" if none can be determined.
func wrappedCommandName(cmd *syntax.CallExpr, prog string) string {
	wrappers := map[string]bool{
		"env": true, "command": true, "sudo": true, "doas": true,
		"nohup": true, "nice": true, "ionice": true, "setsid": true,
		"stdbuf": true, "timeout": true, "xargs": true, "chrt": true,
		"setarch": true, "proot": true,
	}
	if !wrappers[prog] {
		return ""
	}
	// timeout's first non-flag argument is a duration, not a command.
	skipFirstNonFlag := prog == "timeout"
	for i := 1; i < len(cmd.Args); i++ {
		w, ok := wordLiteral(cmd.Args[i])
		if !ok {
			continue
		}
		w = strings.TrimSpace(w)
		if w == "" || strings.HasPrefix(w, "-") {
			continue
		}
		// `env KEY=val cmd` — assignments precede the command.
		if prog == "env" && strings.Contains(w, "=") {
			continue
		}
		if skipFirstNonFlag {
			skipFirstNonFlag = false
			continue
		}
		return basename(strings.TrimPrefix(w, "\\"))
	}
	return ""
}
