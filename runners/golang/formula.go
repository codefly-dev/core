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
	"regexp"
	"slices"
	"strings"
	"syscall"
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
	// EnvErrorNoTestsExecuted / EnvErrorNoTestsMatchedSelectors mirror the
	// python runner's structural zero-case reasons — SAME strings, so
	// language-blind consumers route both runners identically. A run that
	// grades zero cases is never a pass, even on exit 0.
	EnvErrorNoTestsExecuted         = "no-tests-executed"
	EnvErrorNoTestsMatchedSelectors = "no-tests-matched-selectors"
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
// selectors ("./pkg", "example.com/x") REPLACE the command's package scope;
// other selectors are LITERAL test names ("TestFoo", "TestFoo/case") —
// regexp-escaped and anchored into `-run`, so "TestFoo" never selects
// "TestFooBar" and bracketed subtest names stay literal.
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

	args, cmdPkgs := append([]string(nil), command[1:]...), []string(nil)
	// Peel the trailing package pattern off the command so selectors can
	// re-scope it; default back to ./... when absent.
	if n := len(args); n > 0 && isPackagePath(args[n-1]) {
		cmdPkgs = []string{args[n-1]}
		args = args[:n-1]
	}
	var runSelectors, selPkgs []string
	for _, sel := range selectors {
		if sel == "" {
			continue
		}
		if isPackagePath(sel) {
			selPkgs = append(selPkgs, sel)
			continue
		}
		runSelectors = append(runSelectors, sel)
	}
	if pat := buildRunPattern(runSelectors); pat != "" {
		args = append(args, "-run", pat)
	}
	// Package selectors NARROW the run: they replace the command's own
	// package scope (usually ./...) rather than joining it — otherwise
	// ./... would keep matching everything and the selector is a no-op.
	pkgs := selPkgs
	if len(pkgs) == 0 {
		pkgs = cmdPkgs
	}
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}
	// The parser reads only the -json event stream. A supplied formula that
	// omits it (`go test ./...`) would parse zero events and grade an unread
	// run — inject the flag right after the subcommand.
	if len(args) > 0 && args[0] == "test" && !slices.Contains(args, "-json") {
		args = slices.Insert(args, 1, "-json")
	}
	args = append(args, pkgs...)

	cmd := exec.CommandContext(ctx, command[0], args...)
	cmd.Dir = sourceDir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	// `go test` runs each compiled test binary as its own child; killing
	// just the `go` tool on ctx cancellation orphans them mid-test. Kill
	// the whole process group — SIGTERM first, SIGKILL after a grace —
	// mirroring the python twin's RunFormulaStructured.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid := cmd.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		time.AfterFunc(5*time.Second, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
		return nil
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	raw := buf.String()

	structured := ParseTestJSONStructured(raw)
	resp := structured.ToProtoResponse("gotest", "", time.Since(start))
	total := resp.GetCounts().GetTotal()

	// Environment classification runs only when the process failed and the
	// structured stream produced NOTHING to grade — a completed (even failing)
	// test run is never an environment block.
	if runErr != nil && total == 0 {
		msg := "go test errored before any test could execute"
		if reason, detail := ClassifyEnvError(raw, runErr); reason != "" {
			msg = fmt.Sprintf("env-blocked (%s): %s", reason, detail)
		} else if excerpt := firstOutputLine(raw); excerpt != "" {
			// Not environmental (e.g. a compile error in the code under
			// test): surface the first concrete line so the caller can act.
			msg = fmt.Sprintf("go test errored before any test could execute: %s", excerpt)
		}
		return erroredResponse(msg, raw), nil
	}

	// Zero cases on a clean exit is never a pass: `go test` exits 0 when a
	// -run pattern matches nothing or the scoped packages have no test
	// files. Same structural reasons — and reason strings — as the python
	// twin, so language-blind consumers route both identically.
	//
	// TAG CONTRACT: the two zero-case shapes carry DIFFERENT tags because
	// they demand different remediation. Selector-scoped zero-match is the
	// CALLER naming tests that don't exist — the module may be perfectly
	// healthy, so tagging it env-blocked sends the caller repairing an
	// environment that isn't broken. It gets `test-selection-error (...)`:
	// actionable feedback to fix the selection, never an environment claim.
	if total == 0 {
		if len(runSelectors) > 0 || len(selPkgs) > 0 {
			msg := fmt.Sprintf("test-selection-error (%s): selectors %v matched zero tests — the selectors do not name any test in the module",
				EnvErrorNoTestsMatchedSelectors, selectors)
			return erroredResponse(msg, raw), nil
		}
		msg := fmt.Sprintf("env-blocked (%s): test command executed zero tests — a command that discovers nothing is a broken invocation, not a passing run",
			EnvErrorNoTestsExecuted)
		return erroredResponse(msg, raw), nil
	}

	// go test failed but every parsed case passed: an interrupted or killed
	// run whose partial stream happens to be all-green (ctx cancellation
	// kills the run mid-flight). A run that did not complete is never green.
	if runErr != nil && resp.GetResult().GetState() == runtimev0.TestRunResult_PASSED {
		msg := fmt.Sprintf("go test did not run to completion: %v", runErr)
		if ctx.Err() != nil {
			msg = fmt.Sprintf("go test interrupted (%v) with partial results: %v", ctx.Err(), runErr)
		}
		resp.Result = &runtimev0.TestRunResult{State: runtimev0.TestRunResult_ERRORED, Message: msg}
		resp.Status = &runtimev0.TestStatus{State: runtimev0.TestStatus_ERROR, Message: msg}
	}
	return resp, nil
}

// erroredResponse is the shared shape for runs that produced no gradable
// cases: an explicit ERRORED result carrying the classification message and
// the raw stream for diagnosis.
func erroredResponse(msg, raw string) *runtimev0.TestResponse {
	return &runtimev0.TestResponse{
		Status: &runtimev0.TestStatus{State: runtimev0.TestStatus_ERROR, Message: msg},
		Result: &runtimev0.TestRunResult{State: runtimev0.TestRunResult_ERRORED, Message: msg},
		Counts: &runtimev0.TestCounts{},
		Output: raw,
	}
}

// buildRunPattern renders literal test selectors into a `go test -run`
// expression. go test splits the expression on "/" and matches each element
// against the corresponding level of the test name, so every selector
// segment is regexp-escaped and anchored: literal names never over-match
// ("TestFoo" must not select "TestFooBar") and metacharacters in subtest
// names (t.Run("edge [1]")) stay literal.
//
// Multiple selectors are OR'd level by level. When selectors have different
// depths, only their common prefix depth is constrained; same-depth
// selectors constrain every level. Either way the result can over-select
// sibling subtests (a superset) — never miss a selected one.
func buildRunPattern(selectors []string) string {
	if len(selectors) == 0 {
		return ""
	}
	split := make([][]string, 0, len(selectors))
	sameDepth := true
	minDepth := 0
	for i, sel := range selectors {
		segs := strings.Split(sel, "/")
		split = append(split, segs)
		if i == 0 || len(segs) < minDepth {
			minDepth = len(segs)
		}
		if len(segs) != len(split[0]) {
			sameDepth = false
		}
	}
	levels := minDepth
	if sameDepth {
		levels = len(split[0])
	}
	parts := make([]string, levels)
	for i := 0; i < levels; i++ {
		var alts []string
		seen := make(map[string]bool)
		for _, segs := range split {
			q := regexp.QuoteMeta(segs[i])
			if !seen[q] {
				seen[q] = true
				alts = append(alts, q)
			}
		}
		if len(alts) == 1 {
			parts[i] = "^" + alts[0] + "$"
		} else {
			parts[i] = "^(" + strings.Join(alts, "|") + ")$"
		}
	}
	return strings.Join(parts, "/")
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
