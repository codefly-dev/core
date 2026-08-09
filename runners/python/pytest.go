package python

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/codefly-dev/core/resources"
)

// combinePytestK joins multiple test-name patterns into a single
// pytest -k expression. Pytest's -k uses Python boolean syntax
// ("not foo and bar"), so multi-pattern OR is " or " — not "|".
// Returns "" when no patterns are given so callers can omit -k.
func combinePytestK(patterns []string) string {
	if len(patterns) == 0 {
		return ""
	}
	if len(patterns) == 1 {
		return patterns[0]
	}
	return strings.Join(patterns, " or ")
}

// TestSummary holds the parsed results of a pytest run.
type TestSummary struct {
	Run      int32
	Passed   int32
	Failed   int32
	Skipped  int32
	Coverage float32
	Failures []string
}

// SummaryLine formats a one-line summary string.
func (s *TestSummary) SummaryLine() string {
	parts := []string{fmt.Sprintf("%d passed", s.Passed)}
	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", s.Failed))
	}
	if s.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", s.Skipped))
	}
	if s.Coverage > 0 {
		parts = append(parts, fmt.Sprintf("%.1f%% coverage", s.Coverage))
	}
	return strings.Join(parts, ", ")
}

// ParsePytestVerbose parses the verbose text output from pytest -v --tb=short.
func ParsePytestVerbose(output string) *TestSummary {
	s := &TestSummary{}

	var currentFailure strings.Builder
	inFailure := false

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.Contains(trimmed, " PASSED") {
			s.Run++
			s.Passed++
			if inFailure {
				s.Failures = append(s.Failures, currentFailure.String())
				currentFailure.Reset()
				inFailure = false
			}
		} else if strings.Contains(trimmed, " FAILED") {
			s.Run++
			s.Failed++
			if inFailure {
				s.Failures = append(s.Failures, currentFailure.String())
				currentFailure.Reset()
			}
			inFailure = true
			currentFailure.WriteString(fmt.Sprintf("FAIL %s\n", trimmed))
		} else if strings.Contains(trimmed, " SKIPPED") {
			s.Run++
			s.Skipped++
		} else if inFailure {
			currentFailure.WriteString(line + "\n")
		}
	}

	if inFailure {
		s.Failures = append(s.Failures, currentFailure.String())
	}

	return s
}

// TestEvent is a single per-test signal extracted from pytest's verbose
// output. Mirrors the go-side TestEvent shape so consumers can drive a
// uniform UI across both languages.
type TestEvent struct {
	Action string // "pass" | "fail" | "skip"
	Test   string // test node id (e.g. "tests/test_admin.py::test_version")
}

// TestOptions controls pytest invocation.
type TestOptions struct {
	// OnEvent, when non-nil, is called for each per-test result line as
	// pytest emits it. Lets the agent forward live progress to the TUI
	// without waiting for the full summary at the end.
	OnEvent func(TestEvent)

	// CacheDir, when non-empty, persists the raw pytest output to
	// <CacheDir>/last-test.txt for post-mortem debugging.
	CacheDir string

	// Target is a directory or node id (e.g. "tests/unit",
	// "tests/test_admin.py::test_version"). Empty runs the default scope.
	Target string

	// Filters are name patterns passed to pytest's -k expression.
	// Multiple values are joined with " or " — pytest's standard idiom.
	Filters []string

	// Verbose toggles pytest's -v flag. Defaults on at the helper level
	// for backward compat; agents that want quieter output should pass
	// false explicitly via this struct.
	Verbose bool
	// VerboseSet distinguishes "use default" from "explicitly false".
	VerboseSet bool

	// Timeout maps to pytest-timeout's --timeout=<sec>. Empty leaves the
	// default. The adapter materializes that plugin in its isolated environment;
	// projects never need to declare a Codefly implementation dependency.
	// Accepts Go duration syntax ("30s", "2m") which we coerce into seconds.
	Timeout string

	// Coverage enables coverage instrumentation via pytest-cov when the
	// project has it installed. Off by default.
	Coverage bool

	// ExtraArgs are appended verbatim to the pytest command — power-user
	// passthrough for flags codefly does not model.
	ExtraArgs []string
}

// RunPythonTests runs pytest and returns parsed results. Backward-compat
// wrapper: calls RunPythonTestsStructured internally and converts to the
// flat *TestSummary shape so existing consumers keep working unchanged.
//
// New code should call RunPythonTestsStructured directly to get the full
// structured run (per-case file:line, captured-on-failure-only output,
// proto-friendly hierarchy).
func RunPythonTests(ctx context.Context, sourceDir string, envVars []*resources.EnvironmentVariable, opts ...TestOptions) (*TestSummary, error) {
	run, err := RunPythonTestsStructured(ctx, sourceDir, envVars, opts...)
	if run == nil {
		return &TestSummary{}, err
	}
	return run.LegacyTestSummary(), err
}

// RunPythonTestsStructured runs pytest with --junitxml output, parses
// the XML into the SOTA structured run, and returns it. Preferred entry
// point for new code that consumes the structured TestResponse shape.
//
// Pytest's JUnit XML carries per-case file:line, captured stdout/stderr
// in the `<failure>` body for failed tests, and zero-body `<testcase>`
// for passed tests — fits the response-size discipline rule perfectly.
//
// Coverage is scraped from pytest-cov's terminal output (XML doesn't
// carry coverage); the OnEvent callback still works against the
// verbose stream.
func RunPythonTestsStructured(ctx context.Context, sourceDir string, envVars []*resources.EnvironmentVariable, opts ...TestOptions) (*StructuredTestRun, error) {
	var opt TestOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	// Runtime evidence is an adapter artifact, not project source. Keep the
	// JUnit file outside the checkout so a read-only test run never dirties the
	// workspace or depends on its write permissions.
	junitDir, err := os.MkdirTemp("", "codefly-pytest-*")
	if err != nil {
		return nil, fmt.Errorf("create pytest evidence directory: %w", err)
	}
	defer os.RemoveAll(junitDir)
	junitFile := filepath.Join(junitDir, fmt.Sprintf("pytest-junit-%d.xml", time.Now().UnixNano()))

	// ARCHITECTURE: Packaging backends are allowed to materialize metadata next
	// to their input even when uv's environment and cache live elsewhere.
	// Setuptools, for example, creates *.egg-info while resolving both editable
	// projects and local requirements. Execute the observational default test
	// capability against a plugin-owned source snapshot so build backends and
	// tests can never dirty the user's checkout. This copy belongs to the
	// Codefly runtime (the hands), not Mind; it is removed with the evidence
	// directory after the run.
	runtimeSourceDir := filepath.Join(junitDir, "source")
	if err := snapshotSourceTree(sourceDir, runtimeSourceDir); err != nil {
		return nil, fmt.Errorf("snapshot Python source for read-only test execution: %w", err)
	}

	// ARCHITECTURE: The default pytest adapter is still a real formula. Build it
	// through the same project-derived provisioning contract as an explicitly
	// declared formula so the two production paths cannot drift. In particular,
	// --no-project isolates the checkout but does NOT mean "ignore the project's
	// requirements": requirement files, editable packaging, interpreter pins,
	// groups, and extras remain project-owned input to uv.
	spec := SpecFromFormula(
		[]string{"pytest"},
		OutputJUnitXML,
		nil,
		DeriveProvisioning(runtimeSourceDir),
		nil,
	)
	spec.Env = append(spec.Env, envVars...)
	spec.ExtraArgs = append(spec.ExtraArgs, "--tb=short", "-p", "no:cacheprovider")

	// Default to verbose unless the caller explicitly set Verbose=false.
	// Verbose feeds the OnEvent stream; the JUnit XML is parsed regardless.
	if !opt.VerboseSet || opt.Verbose {
		spec.ExtraArgs = append(spec.ExtraArgs, "-v")
	}

	// Filters → -k "p1 or p2 or ..."  (pytest's expression syntax).
	if expr := combinePytestK(opt.Filters); expr != "" {
		spec.ExtraArgs = append(spec.ExtraArgs, "-k", expr)
	}

	// Timeout — pytest-timeout reads --timeout=<seconds>. Convert Go
	// durations to seconds; pass through anything else verbatim so users can
	// supply already-formatted values. This is adapter-owned runner knowledge:
	// materialize the real plugin in uv's isolated environment instead of
	// requiring every project to carry it or retrying without the typed bound.
	if opt.Timeout != "" {
		if !containsRequirement(spec.With, "pytest-timeout") {
			spec.With = append(spec.With, "pytest-timeout")
		}
		if d, err := time.ParseDuration(opt.Timeout); err == nil {
			spec.ExtraArgs = append(spec.ExtraArgs, fmt.Sprintf("--timeout=%d", int(d.Seconds())))
		} else {
			spec.ExtraArgs = append(spec.ExtraArgs, "--timeout="+opt.Timeout)
		}
	}

	// Coverage — pytest-cov, scoped to the source tree so we report
	// numbers for the user's code rather than the test files themselves.
	if opt.Coverage {
		spec.ExtraArgs = append(spec.ExtraArgs, "--cov=.", "--cov-report=term")
	}

	// Power-user passthrough.
	spec.ExtraArgs = append(spec.ExtraArgs, opt.ExtraArgs...)

	// Target last — pytest treats positional args as collection paths.
	if opt.Target != "" {
		spec.Selectors = append(spec.Selectors, opt.Target)
	}

	// Keep the adapter environment outside the checkout. Requirement files and
	// editable packages work with --no-project. Dependency groups and extras
	// require pyproject discovery, so those runs use uv's isolated project mode:
	// it resolves the declared sets without creating uv.lock or .venv beside the
	// user's source. Persistent formula environments remain an explicit formula
	// concern and are never introduced by this read-only default adapter.
	if spec.Editable && spec.EditableTarget == "" {
		if abs, absErr := filepath.Abs(runtimeSourceDir); absErr == nil {
			spec.EditableTarget = abs
		}
	}
	projectIsolated := len(spec.DependencyGroups) > 0 || len(spec.Extras) > 0
	if projectIsolated {
		spec.NoProject = false
	}
	pytestArgs := BuildUvArgs(spec, junitFile)
	if projectIsolated {
		pytestArgs = append([]string{"run", "--isolated"}, pytestArgs[1:]...)
	}

	cmd := exec.CommandContext(ctx, "uv", pytestArgs...)
	cmd.Dir = runtimeSourceDir

	// Run pytest in its own process group so a cancelled/timed-out run kills
	// the WHOLE tree (uv → pytest → xdist workers), not just the direct child.
	// Bare exec.CommandContext only SIGKILLs `uv`, orphaning the pytest workers
	// — the historical zombie/port-leak class. Cancel tree-signals the group:
	// SIGTERM for a graceful unwind, then SIGKILL after a grace period.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid := cmd.Process.Pid // == pgid because of Setpgid
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		time.AfterFunc(5*time.Second, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
		return nil
	}

	var raw bytes.Buffer
	// Tee stdout: capture to buffer + line-scan for events when a sink is set.
	if opt.OnEvent != nil {
		pr, pw := io.Pipe()
		// One writer value for BOTH streams so os/exec serializes writes to the
		// shared buffer (it uses a single goroutine when Stdout == Stderr).
		// Two distinct MultiWriters raced on the bytes.Buffer.
		combined := io.MultiWriter(&raw, pw)
		cmd.Stdout = combined
		cmd.Stderr = combined
		go scanPytestEvents(pr, opt.OnEvent)
		defer pw.Close()
	} else {
		cmd.Stdout = &raw
		cmd.Stderr = &raw
	}

	// Python bytecode, pytest cache, coverage data, and JUnit evidence are
	// validation artifacts. Keep all of them outside the source checkout.
	cmd.Env = append(os.Environ(),
		"PYTHONPYCACHEPREFIX="+filepath.Join(junitDir, "pycache"),
		"COVERAGE_FILE="+filepath.Join(junitDir, "coverage"),
	)
	for _, ev := range spec.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", ev.Key, ev.Value))
	}

	runErr := cmd.Run()
	rawStr := raw.String()

	// Parse JUnit XML. If pytest didn't write one (collection error,
	// pytest itself crashed), the structured run will be empty —
	// runErr indicates something went wrong; the caller surfaces it.
	xmlBytes, _ := os.ReadFile(junitFile) //nolint:gosec // private temporary path
	coverage := scrapeCoverageFromOutput(rawStr)
	run := ParsePytestJUnit(string(xmlBytes), coverage)
	run.RawOutput = rawStr
	// Match formula execution's typed unhappy-path contract. A collection or
	// provisioning failure that produces zero cases is an environment error,
	// not an opaque process exit for the caller to flatten. Likewise, an empty
	// successful invocation is never a passing test run.
	if run.caseCount() == 0 {
		hasSelection := opt.Target != "" || len(opt.Filters) > 0
		if hasSelection && defaultPytestSelectionMiss(rawStr, runErr) {
			run.EnvError = &RunEnvError{
				Reason: EnvErrorNoTestsMatchedSelectors,
				Detail: defaultPytestSelectionDescription(opt) + " matched zero tests — the selection does not name any collectible test",
			}
		} else if runErr != nil {
			run.EnvError = ClassifyEnvError(rawStr, runErr)
		} else if hasSelection {
			run.EnvError = &RunEnvError{
				Reason: EnvErrorNoTestsMatchedSelectors,
				Detail: defaultPytestSelectionDescription(opt) + " matched zero tests — the selection does not name any collectible test",
			}
		} else {
			run.EnvError = &RunEnvError{
				Reason: EnvErrorNoTestsExecuted,
				Detail: "the default pytest adapter executed zero tests — fix the project test declarations or collection environment",
			}
		}
		runErr = nil
	}

	if opt.CacheDir != "" {
		if err := writeLastTestOutput(opt.CacheDir, rawStr); err != nil {
			// Best-effort persistence — never mask the real result.
			_ = err
		}
	}

	return run, runErr
}

// defaultPytestSelectionMiss recognizes pytest's own zero-match signals before
// generic environment classification. Pytest returns a non-zero process exit
// both when a selector names no test and when collection cannot start; the
// typed result must keep those states distinct so callers repair the selector
// instead of trying to heal a healthy runtime.
func defaultPytestSelectionMiss(raw string, runErr error) bool {
	lower := strings.ToLower(raw)
	for _, marker := range []string{
		"file or directory not found:",
		"error: not found:",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	type exitCoder interface{ ExitCode() int }
	exitErr, ok := runErr.(exitCoder)
	return ok && exitErr.ExitCode() == 5 // pytest: no tests were collected
}

func defaultPytestSelectionDescription(opt TestOptions) string {
	parts := make([]string, 0, 2)
	if opt.Target != "" {
		parts = append(parts, fmt.Sprintf("target %q", opt.Target))
	}
	if len(opt.Filters) > 0 {
		parts = append(parts, fmt.Sprintf("filters %q", opt.Filters))
	}
	return strings.Join(parts, " with ")
}

// snapshotSkipDirs names directories that never belong in the read-only test
// snapshot: virtualenvs, tool caches, and foreign-ecosystem trees. They are
// regenerable and often large, and the fresh uv run rebuilds its own
// environment rather than reading them. Version-control directories are
// deliberately NOT listed: build backends that derive a dynamic version
// (setuptools_scm and friends) read .git during the build, so dropping it would
// fail the very run we are isolating. Prior *.egg-info directories ARE skipped
// (by suffix) so stale build metadata cannot shadow what the run regenerates.
var snapshotSkipDirs = map[string]bool{
	".venv":         true,
	"venv":          true,
	".tox":          true,
	".nox":          true,
	"__pycache__":   true,
	".pytest_cache": true,
	".mypy_cache":   true,
	".ruff_cache":   true,
	"node_modules":  true,
}

// snapshotSourceTree copies the Python source at src into dst for a read-only
// test run. It exists instead of os.CopyFS for three reasons the default
// adapter must survive on real checkouts: it resolves a symlinked source root
// (WalkDir alone would yield the root as a lone symlink entry and copy nothing,
// where os.DirFS/os.CopyFS follow it); it skips the regenerable venv/cache and
// prior build-metadata directories in snapshotSkipDirs so stale state cannot
// leak in and a full checkout is not duplicated per run; and it skips irregular
// files (sockets, FIFOs, devices) instead of aborting the whole copy — a single
// stray socket in a checkout must not break test validation. Regular files
// preserve their mode; symlinks inside the tree are recreated verbatim.
func snapshotSourceTree(src, dst string) error {
	root, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			name := entry.Name()
			if rel != "." && (snapshotSkipDirs[name] || strings.HasSuffix(name, ".egg-info")) {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			linkDest, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(linkDest, target)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		return copyRegularFile(p, target, entry)
	})
}

// copyRegularFile copies a single regular file, preserving its permission bits.
func copyRegularFile(src, dst string, entry fs.DirEntry) error {
	info, err := entry.Info()
	if err != nil {
		return err
	}
	in, err := os.Open(src) //nolint:gosec // snapshotting a plugin-owned source tree
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// scrapeCoverageFromOutput extracts the total coverage percentage from
// pytest-cov's terminal output. Looks for the "TOTAL ... NN%" line
// pytest-cov emits with --cov-report=term.
func scrapeCoverageFromOutput(raw string) float32 {
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "TOTAL") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		last := strings.TrimSuffix(fields[len(fields)-1], "%")
		var pct float64
		if _, err := fmt.Sscanf(last, "%f", &pct); err == nil {
			return float32(pct)
		}
	}
	return 0
}

// scanPytestEvents reads pytest's verbose output line by line and emits
// a TestEvent for each PASSED / FAILED / SKIPPED line. Pytest prints
// these as `tests/admin/test_admin.py::test_version PASSED [100%]`.
//
// Best-effort parser — non-matching lines are silently skipped so the
// callback only sees structured signal.
func scanPytestEvents(r io.Reader, onEvent func(TestEvent)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // tolerate long traceback lines
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.Contains(line, " PASSED"):
			onEvent(TestEvent{Action: "pass", Test: pytestNode(line, " PASSED")})
		case strings.Contains(line, " FAILED"):
			onEvent(TestEvent{Action: "fail", Test: pytestNode(line, " FAILED")})
		case strings.Contains(line, " SKIPPED"):
			onEvent(TestEvent{Action: "skip", Test: pytestNode(line, " SKIPPED")})
		}
	}
}

// pytestNode extracts the test node id (everything before the marker)
// from a pytest progress line.
func pytestNode(line, marker string) string {
	idx := strings.Index(line, marker)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(line[:idx])
}

// writeLastTestOutput dumps the raw pytest output to
// <cacheDir>/last-test.txt. Atomic via tmp + rename.
func writeLastTestOutput(cacheDir, raw string) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(cacheDir, "last-test.txt")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(raw), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RunPythonLint runs read-only Ruff checks and returns the output. target is a
// service-relative file/directory when present; an option-looking target is
// rejected by Ruff after the explicit "--" separator rather than being
// interpreted as a new command flag.
func RunPythonLint(ctx context.Context, sourceDir string, targets ...string) (string, error) {
	target := "."
	if len(targets) > 0 && targets[0] != "" {
		target = targets[0]
	}
	// Ruff configuration is still discovered from sourceDir, but uv's project
	// mode is disabled so a read-only lint never creates uv.lock or .venv in
	// the checkout.
	cmd := exec.CommandContext(ctx, "uv", "run", "--no-project", "--with", "ruff", "ruff", "check", "--", target)
	cmd.Dir = sourceDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// RunPythonBuild performs the Python compile gate without requiring a valid
// package declaration. Source-only repositories and partially edited
// pyproject.toml files still need syntax validation, so --no-project keeps the
// check independent from dependency materialization.
func RunPythonBuild(ctx context.Context, sourceDir, target string) (string, error) {
	if target == "" {
		target = "."
	}
	bytecodeDir, err := os.MkdirTemp("", "codefly-python-bytecode-*")
	if err != nil {
		return "", fmt.Errorf("create Python bytecode directory: %w", err)
	}
	defer os.RemoveAll(bytecodeDir)
	cmd := exec.CommandContext(ctx, "uv", "run", "--no-project", "python", "-m", "compileall", "-q", "--", target)
	cmd.Dir = sourceDir
	cmd.Env = append(os.Environ(), "PYTHONPYCACHEPREFIX="+bytecodeDir)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	return out.String(), err
}
