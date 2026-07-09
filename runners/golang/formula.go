package golang

// formula.go — the MODULE-LOCAL Go test formula runner: the golang twin of
// runners/python.RunFormulaStructured. It exists so a language-blind brain
// (Mind) can ship a runtimev0.TestFormula (or nothing at all) and have THIS
// plugin own "how to test a Go module": derive `go test -json ./...` from
// go.mod, execute it in the module directory, parse the structured event
// stream, and — critically — CLASSIFY environment breakage (go.mod parse
// errors, toolchain missing, module resolution) distinctly from test
// failures, using the same explicit `env-blocked (<reason>): <detail>`
// result-message tag the python runner uses. Consumers detect an environment
// block from the tag (or ERRORED+zero-cases) without any Go-specific logic.
//
// This is deliberately NOT GoRunnerEnvironment: that type manages agent
// service lifecycles (caching, docker/nix, binary builds). A formula run is a
// one-shot `go test` in a checkout.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// OutputGoTestJSON is the formula output format produced by `go test -json`.
const OutputGoTestJSON = "gotest-json"

// Environment-block reasons for Go module runs. They appear inside the
// `env-blocked (<reason>): <detail>` result message.
const (
	// EnvErrorModuleBroken: the module metadata itself prevents any build or
	// test — go.mod parse errors, unsupported go directive, missing go.sum
	// entries, unresolvable module requirements. Repairing the module
	// metadata is an ENVIRONMENT action, not a code fix.
	EnvErrorModuleBroken = "go-module-broken"
	// EnvErrorToolchainMissing: the `go` binary is not available to the
	// runner at all.
	EnvErrorToolchainMissing = "go-toolchain-missing"
)

// DeriveFormula derives the module-local test formula for a Go module:
// `go test -json ./...` when sourceDir contains a go.mod. Mirrors
// python.DeriveFormula's contract (ok=false when this plugin does not own
// the project).
func DeriveFormula(sourceDir string) (cmd []string, output string, ok bool) {
	if _, err := os.Stat(filepath.Join(sourceDir, "go.mod")); err != nil {
		return nil, "", false
	}
	return []string{"go", "test", "-json", "./..."}, OutputGoTestJSON, true
}

// IsGoFormula reports whether a supplied formula command belongs to this
// runner (a `go` invocation).
func IsGoFormula(command []string) bool {
	return len(command) > 0 && filepath.Base(command[0]) == "go"
}

// ClassifyEnvError decides whether a failed run was blocked by the
// ENVIRONMENT (reason != "") rather than failing tests or broken user code.
// The classification is deliberately narrow: module metadata / toolchain /
// module-resolution problems are environmental; a compile error in the code
// under test is NOT (that is the code's fault and an edit can fix it).
func ClassifyEnvError(raw string, runErr error) (reason, detail string) {
	if runErr != nil {
		var execErr *exec.Error
		if errors.As(runErr, &execErr) {
			return EnvErrorToolchainMissing, execErr.Error()
		}
	}
	for _, marker := range []string{
		"errors parsing go.mod",
		"unknown directive:",
		"invalid go version",
		"go.mod requires go >=",
		"missing go.sum entry",
		"no required module provides package",
		"cannot find module providing",
		"cannot load module",
		"unsupported toolchain",
	} {
		if idx := strings.Index(raw, marker); idx >= 0 {
			rest := raw[idx:]
			line := rest
			if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
				line = rest[:nl]
				// A header line ending in ":" ("errors parsing go.mod:")
				// carries the actionable detail on the NEXT line — keep it.
				if strings.HasSuffix(strings.TrimSpace(line), ":") {
					next := rest[nl+1:]
					if nl2 := strings.IndexByte(next, '\n'); nl2 >= 0 {
						next = next[:nl2]
					}
					line = line + " " + strings.TrimSpace(next)
				}
			}
			return EnvErrorModuleBroken, strings.TrimSpace(line)
		}
	}
	return "", ""
}

// RunFormula executes a Go test formula in sourceDir and returns the
// structured proto response. Command may be empty (derived via
// DeriveFormula). Selectors follow the shared convention: package-shaped
// selectors ("./pkg", "example.com/x") scope the packages under test; other
// selectors are test-name patterns OR'd into `-run`.
//
// The run always sets GOWORK=off: a formula run tests THIS module in
// isolation, and a go.work in any parent directory (common when fixtures
// live inside a bigger repo) must not leak into module resolution.
func RunFormula(ctx context.Context, sourceDir string, command []string, selectors []string) (*runtimev0.TestResponse, error) {
	start := time.Now()
	if len(command) == 0 {
		derived, _, ok := DeriveFormula(sourceDir)
		if !ok {
			return nil, fmt.Errorf("go formula: %s has no go.mod — not a Go module", sourceDir)
		}
		command = derived
	}

	args, pkgs := append([]string(nil), command[1:]...), []string(nil)
	// Peel the trailing package pattern off the command so selectors can
	// re-scope it; default back to ./... when absent.
	if n := len(args); n > 0 && isPackagePath(args[n-1]) {
		pkgs = []string{args[n-1]}
		args = args[:n-1]
	}
	var runPatterns []string
	for _, sel := range selectors {
		if sel == "" {
			continue
		}
		if isPackagePath(sel) {
			pkgs = append(pkgs, sel)
			continue
		}
		runPatterns = append(runPatterns, sel)
	}
	if pat := combineRunRegex(runPatterns); pat != "" {
		args = append(args, "-run", pat)
	}
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}
	args = append(args, pkgs...)

	cmd := exec.CommandContext(ctx, command[0], args...)
	cmd.Dir = sourceDir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	raw := buf.String()

	structured := ParseTestJSONStructured(raw)
	resp := structured.ToProtoResponse("gotest", "", time.Since(start))

	// Environment classification runs only when the process failed and the
	// structured stream produced NOTHING to grade — a completed (even failing)
	// test run is never an environment block.
	if runErr != nil && resp.GetCounts().GetTotal() == 0 {
		msg := "go test errored before any test could execute"
		if reason, detail := ClassifyEnvError(raw, runErr); reason != "" {
			msg = fmt.Sprintf("env-blocked (%s): %s", reason, detail)
		} else if excerpt := firstOutputLine(raw); excerpt != "" {
			// Not environmental (e.g. a compile error in the code under
			// test): surface the first concrete line so the caller can act.
			msg = fmt.Sprintf("go test errored before any test could execute: %s", excerpt)
		}
		return &runtimev0.TestResponse{
			Status: &runtimev0.TestStatus{State: runtimev0.TestStatus_ERROR, Message: msg},
			Result: &runtimev0.TestRunResult{State: runtimev0.TestRunResult_ERRORED, Message: msg},
			Counts: &runtimev0.TestCounts{},
			Output: raw,
		}, nil
	}
	return resp, nil
}

// firstOutputLine returns the first non-empty, non-JSON line of a raw go
// test stream — the human-readable error go prints before/instead of events.
func firstOutputLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "{") {
			continue
		}
		return line
	}
	return ""
}
