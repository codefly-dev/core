package python

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// MaxCapturedOutputBytesPerCase mirrors the Go-runner cap. Pytest's
// JUnit `<failure>` elements bundle the traceback + captured
// stdout/stderr in the body — typically a few KB per case; very
// rarely exceeds 32KiB unless the test prints debug logs.
const MaxCapturedOutputBytesPerCase = 32 * 1024

// junitTestsuites is the outer wrapper pytest emits when run with
// --junitxml. Some pytest configs emit a single `<testsuite>` at the
// top instead of `<testsuites>`; we accept both shapes via the
// fallback path in ParsePytestJUnit.
type junitTestsuites struct {
	XMLName xml.Name         `xml:"testsuites"`
	Suites  []junitTestsuite `xml:"testsuite"`
}

type junitTestsuite struct {
	XMLName   xml.Name        `xml:"testsuite"`
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Errors    int             `xml:"errors,attr"`
	Skipped   int             `xml:"skipped,attr"`
	Time      float64         `xml:"time,attr"`
	Timestamp string          `xml:"timestamp,attr"`
	Cases     []junitTestcase `xml:"testcase"`
}

type junitTestcase struct {
	XMLName   xml.Name     `xml:"testcase"`
	ClassName string       `xml:"classname,attr"`
	Name      string       `xml:"name,attr"`
	Time      float64      `xml:"time,attr"`
	File      string       `xml:"file,attr"`
	Line      int          `xml:"line,attr"`
	Failure   *junitDetail `xml:"failure,omitempty"`
	Error     *junitDetail `xml:"error,omitempty"`
	Skipped   *junitDetail `xml:"skipped,omitempty"`
	SystemOut string       `xml:"system-out,omitempty"`
	SystemErr string       `xml:"system-err,omitempty"`
}

// junitDetail is the body of a `<failure>`/`<error>`/`<skipped>` tag.
// `message` is the one-liner pytest renders in the summary; the body
// contains the traceback + captured output.
type junitDetail struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Body    string `xml:",chardata"`
}

// StructuredTestRun is the python-side equivalent of the Go runner's
// StructuredTestRun. Built by ParsePytestJUnit; convertible to
// runtimev0.TestResponse via ToProtoResponse.
type StructuredTestRun struct {
	// Suites — one per source file. Pytest's JUnit emits a flat list
	// of `<testcase>`s; we group by file to mirror the Go runner's
	// "one suite per package" shape. Source-file grouping is what
	// developers actually reason about ("did the users tests pass?").
	Suites []*StructuredSuite

	// CoveragePct is scraped from pytest-cov's terminal output passed
	// in alongside the JUnit XML. JUnit XML doesn't carry coverage.
	CoveragePct float32

	// RawOutput preserves the process output even when no test cases were parsed.
	// Zero-case environment blocks often have no suite/case payload, but the raw
	// install/import error is the evidence a caller needs to heal the environment.
	RawOutput string

	// truncatedCases tracks how many cases had their captured_output
	// trimmed by the per-case cap.
	truncatedCases int32

	// EnvError, when set, means the run could NOT EXECUTE the tests — the
	// ENVIRONMENT was blocked (a dependency failed to install, the project failed
	// to import, the interpreter is missing) — as opposed to tests running and
	// failing. RunFormulaStructured sets it whenever the run produced ZERO cases
	// (REGARDLESS of exit code: a command that discovers nothing and exits 0 is a
	// broken invocation, never a pass). ToProtoResponse maps it to
	// Result.State=ERRORED with a classified reason, so a caller distinguishes
	// "tests failed" (FAILED) from "couldn't run" (ERRORED) from the STRUCTURE —
	// never from a raw "exit status 1". This is what the Mind tooling inner loop
	// reads to heal plugin config.
	EnvError *RunEnvError

	// Summary carries aggregate counts parsed from a unittest runner's
	// "Ran N tests / OK (skipped=…)" trailer when DEFAULT verbosity emitted no
	// per-test lines. Present only for the case-less unittest path; nil for
	// pytest/JUnit and verbose unittest (where Suites carry every case).
	Summary *UnittestSummary

	// Materialized is true when the run proved the ENVIRONMENT is usable — uv
	// resolved and built the venv, the project imported, and the test RUNNER
	// launched into execution — even though ZERO cases completed because the run
	// was cut off by the caller's budget (a deadline/cancel), not by a project
	// failure. This is the exact signal an environment PRE-WARM needs: "can this
	// env execute the project's tests?" is answered YES the moment the runner
	// starts, without waiting for a multi-thousand-test suite (django's 7757
	// tests) to finish. Distinct from EnvError (blocked) and from a completed
	// run (cases > 0). ToProtoResponse surfaces it as a healthy, non-errored
	// result carrying EnvMaterializedMessagePrefix.
	Materialized bool
}

// EnvMaterializedMessagePrefix marks a TestResponse whose run MATERIALIZED the
// environment (runner launched) but was budget-interrupted before any case
// completed. Mind's health/pre-warm probe imports this constant and treats such
// a result as HEALTHY rather than re-parsing output — codefly stays the single
// owner of "what does a materialized-but-incomplete run look like".
const EnvMaterializedMessagePrefix = "environment-materialized: "

// environmentExecutionMarkers are framework-agnostic proof that the test RUNNER
// launched past environment setup (uv build + project import) into execution.
// Their presence means the environment is usable regardless of whether the run
// finished. Lowercased-substring matched against raw output.
// These are UNAMBIGUOUS test-runner signals — they appear only after uv
// resolved the env and the project imported. Bare "passed"/"failed" are
// deliberately excluded: they false-match build output ("Failed to build
// numpy"), which would launder a genuine build failure into a healthy env.
var environmentExecutionMarkers = []string{
	"creating test database",   // django/unittest runner DB setUp
	"destroying test database", // django teardown
	"test session starts",      // pytest banner
	"collected ",               // pytest collection ("collected 42 items")
	"rootdir:",                 // pytest header
	"ran ",                     // unittest summary ("Ran 12 tests")
	"platform ",                // pytest env line ("platform darwin -- Python…")
	"tests in ",                // unittest timing ("... tests in 3.2s")
}

// EnvironmentMaterialized reports whether raw output proves the test runner
// launched into execution (env is usable). Used to distinguish a
// budget-interrupted-but-healthy run from a genuine env block.
func EnvironmentMaterialized(raw string) bool {
	low := strings.ToLower(raw)
	for _, m := range environmentExecutionMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// IsEnvironmentMaterializedMessage reports whether a TestResponse message was
// produced by a materialized-but-incomplete run (Mind's health probe reads
// this instead of re-parsing output).
func IsEnvironmentMaterializedMessage(msg string) bool {
	return strings.HasPrefix(strings.TrimSpace(msg), strings.TrimSpace(EnvMaterializedMessagePrefix))
}

// RunEnvError classifies why the environment blocked the run. The python plugin
// owns this classification (it knows pytest/uv exit semantics); Mind maps Reason
// onto its BlockReason without re-parsing raw output.
type RunEnvError struct {
	// Reason is one of: "missing-dependency" | "import-error" |
	// "version-conflict" | "interpreter-missing" | "test-collection-error" |
	// EnvErrorNoTestsExecuted | EnvErrorNoTestsMatchedSelectors |
	// EnvErrorInvalidCwd | "unknown".
	Reason string
	Detail string // the scraped failure tail, e.g. "ModuleNotFoundError: No module named 'werkzeug'"
}

// Structural env-error reasons emitted by RunFormulaStructured itself (not
// scraped from output). Exported so healers/callers can route on them.
const (
	// EnvErrorNoTestsExecuted: the run produced ZERO test cases with no
	// selectors supplied — the invocation itself discovers nothing (a bare
	// `python`, a wrong cwd/output format). NEVER a pass, even with exit 0.
	EnvErrorNoTestsExecuted = "no-tests-executed"
	// EnvErrorNoTestsMatchedSelectors: selectors were supplied and matched
	// zero tests. Distinct from EnvErrorNoTestsExecuted so a healer knows the
	// command may be fine and the SELECTION is what's wrong.
	EnvErrorNoTestsMatchedSelectors = "no-tests-matched-selectors"
	// EnvErrorInvalidCwd: the formula's provisioning "cwd" is absolute,
	// escapes the code unit, or does not exist.
	EnvErrorInvalidCwd = "invalid-cwd"
	// EnvErrorProvisioningFailed: building the persistent venv (uv venv / uv pip
	// install of the editable project + deps) failed — a real environment block
	// the healer can act on (wrong python, missing build dep, compile error).
	EnvErrorProvisioningFailed = "provisioning-failed"
)

// UnittestSummary is the aggregate a default-verbosity unittest runner prints
// ("Ran N tests in Xs" + status line) when it emits no per-test result lines.
type UnittestSummary struct {
	Total, Passed, Failed, Errored, Skipped int
}

// caseCount returns the number of tests the run EXECUTED — from parsed cases,
// or from the unittest aggregate summary when default verbosity printed none.
// 0 means nothing executed (the env-block signal); a run that executed tests
// (even all-skipped) must NOT read as 0.
func (r *StructuredTestRun) caseCount() int {
	n := 0
	for _, s := range r.Suites {
		n += len(s.Cases)
	}
	if n == 0 && r.Summary != nil {
		return r.Summary.Total
	}
	return n
}

// ClassifyEnvError inspects a run's raw output (and exit error) to decide WHY the
// environment blocked the run. Pattern order is most-specific first. Exported so
// RunFormulaStructured and tests share one classifier.
//
// Detail discipline: the detail is what a REMEDIATOR (LLM or human) acts on, so
// it must carry the actual error line(s) — never a uv download/progress line
// ("Downloaded numpy", "Resolved 25 packages") that happens to sit at the tail
// of the output. Two real regressions locked by tests: a killed sklearn source
// build classified `unknown: Downloaded numpy`, and a django flat-layout build
// error classified `unknown: 'setup.py' are present in the directory` (a
// wrapped fragment of the real setuptools error).
func ClassifyEnvError(raw string, runErr error) *RunEnvError {
	low := strings.ToLower(raw)
	detail := lastMeaningfulLine(raw)
	switch {
	case strings.Contains(low, "syntaxerror"),
		strings.Contains(low, "indentationerror"),
		strings.Contains(low, "taberror"):
		// A parse error in the TEST/source CODE — the tests could not be
		// COLLECTED because the code is broken (e.g. a malformed test edit that
		// inserted a function mid-body). This is NOT an environment problem: the
		// classic "collection error" → import-error bucket would WRONGLY send the
		// tooling loop to "fix provisioning" (it can't fix a SyntaxError) and would
		// loop forever. A distinct reason keeps it ERRORED (so it never false-
		// passes) while telling the loop NOT to heal and telling the agent the
		// real defect: fix the code. Checked FIRST so it wins over the generic
		// "collection error" pattern below.
		return &RunEnvError{Reason: "test-collection-error", Detail: detail}
	case strings.Contains(low, "no matching distribution"),
		strings.Contains(low, "could not find a version"),
		strings.Contains(low, "resolutionimpossible"),
		strings.Contains(low, "no solution found"),
		strings.Contains(low, "unsatisfiable"),
		strings.Contains(low, "version conflict"),
		strings.Contains(low, "incompatible"):
		// A dependency-RESOLUTION conflict (uv/pip). The single last line is
		// often useless ("your requirements are unsatisfiable") — the package
		// names + version bounds that caused it (e.g. "flask depends on
		// Werkzeug<2.1") live in the lines ABOVE. Capture that block so a
		// remediator can actually know WHAT to pin.
		return &RunEnvError{Reason: "version-conflict", Detail: resolutionDetail(raw, detail)}
	case strings.Contains(low, "modulenotfounderror"),
		strings.Contains(low, "no module named"):
		return &RunEnvError{Reason: "missing-dependency", Detail: matchedErrorLine(raw, detail, "modulenotfounderror", "no module named")}
	case strings.Contains(low, "importerror"),
		strings.Contains(low, "cannot import name"),
		strings.Contains(low, "collection error"):
		return &RunEnvError{Reason: "import-error", Detail: matchedErrorLine(raw, detail, "importerror", "cannot import name")}
	case strings.Contains(low, "failed to build"),
		strings.Contains(low, "failed building wheel"),
		strings.Contains(low, "build backend returned an error"),
		strings.Contains(low, "metadata-generation-failed"),
		strings.Contains(low, "flat-layout"),
		strings.Contains(low, "top-level packages discovered"),
		strings.Contains(low, "top-level modules discovered"):
		// A BUILD failure (uv building the project or a source dependency —
		// setuptools backend errors, flat-layout/multiple-top-level-packages
		// discovery refusals, C-extension compile failures). uv wraps these in
		// multi-line `× Failed to build …` / `╰─▶ …` blocks whose LAST line is
		// often a wrapped fragment ("'setup.py' are present in the directory"),
		// so the detail is the multi-line error tail, not one line. Checked
		// AFTER missing-dependency/import-error: a build that failed because a
		// build dep is missing classifies as the more actionable reason.
		return &RunEnvError{Reason: "build-failed", Detail: resolutionDetail(raw, detail)}
	case strings.Contains(low, "no interpreter"),
		strings.Contains(low, "command not found"),
		strings.Contains(low, "not found in"),
		strings.Contains(low, "no virtual environment"):
		return &RunEnvError{Reason: "interpreter-missing", Detail: matchedErrorLine(raw, detail, "no interpreter", "command not found", "not found in", "no virtual environment")}
	default:
		// Unknown: keep the last non-progress LINES (plural) — enough tail for a
		// remediator to see the real error, with download/progress noise
		// filtered. A killed/truncated run can be ALL progress lines; then the
		// exit error is the only truthful detail.
		detail = resolutionDetail(raw, detail)
		if detail == "" && runErr != nil {
			detail = runErr.Error()
		}
		return &RunEnvError{Reason: "unknown", Detail: detail}
	}
}

// matchedErrorLine returns the LAST raw line containing one of the (lowercase)
// patterns that made the classifier pick a reason — the full relevant error
// line ("ModuleNotFoundError: No module named 'numpy'"), not whatever line
// happens to be at the tail of the output. Falls back to the supplied detail.
func matchedErrorLine(raw, fallback string, patterns ...string) string {
	const capLen = 400
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		lowLine := strings.ToLower(line)
		for _, p := range patterns {
			if strings.Contains(lowLine, p) {
				if len(line) > capLen {
					return line[:capLen]
				}
				return line
			}
		}
	}
	return fallback
}

// envFailureSignature matches a line indicating a DEPENDENCY/ENVIRONMENT
// incompatibility — a package missing, or installed but incompatible (a symbol
// moved/removed in a newer version). These originate in site-packages / conftest
// / fixtures, NOT in a test's own assertions.
var envFailureSignature = regexp.MustCompile(`(?i)(ModuleNotFoundError|No module named|ImportError|cannot import name|AttributeError: module .* has no attribute)`)

// detectSharedEnvFailure reports a version/import env block when the SAME
// dependency-incompatibility error repeats across MANY test cases. That is the
// signature of a mis-resolved package — e.g. werkzeug 3.x removed
// `werkzeug.__version__`, so every test that builds a Flask test client raises
// the identical AttributeError — rather than a genuine test failure. A PARTIAL
// run (the tests that don't touch the broken dep still pass) hides this from the
// zero-collected env-block detector, leaving it to masquerade as an ordinary
// "53 failed". We catch it from the repeated error so the tooling inner loop can
// heal the package pin. Returns nil unless one env error dominates (>=3 cases).
func detectSharedEnvFailure(raw string) *RunEnvError {
	counts := map[string]int{}
	for _, line := range strings.Split(raw, "\n") {
		l := strings.TrimSpace(line)
		l = strings.TrimSpace(strings.TrimPrefix(l, "E ")) // pytest error-line prefix
		if envFailureSignature.MatchString(l) {
			counts[l]++
		}
	}
	top, n := "", 0
	for l, c := range counts {
		if c > n {
			top, n = l, c
		}
	}
	if n < 3 {
		return nil
	}
	reason := "import-error"
	low := strings.ToLower(top)
	switch {
	case strings.Contains(low, "no module named"), strings.Contains(low, "modulenotfounderror"):
		reason = "missing-dependency"
	case strings.Contains(low, "has no attribute"):
		// Installed but INCOMPATIBLE: the symbol exists in the version the project
		// expects but was removed/moved in the (newer) version that got resolved —
		// a version mismatch the remediator heals by pinning the package down.
		reason = "version-conflict"
	}
	return &RunEnvError{Reason: reason, Detail: fmt.Sprintf("%s (×%d test cases — shared dependency incompatibility, not a test failure)", top, n)}
}

// resolutionDetail returns a richer detail for a dependency-RESOLUTION conflict:
// the last few non-blank lines (capped), which for uv/pip carry the package
// names + version bounds that caused the conflict (e.g. "flask depends on
// werkzeug>=2.0,<2.1"). uv's single last line — "your requirements are
// unsatisfiable" — names NOTHING, so a remediator can't know what to pin from it
// alone; the explanation lives in the lines above. Falls back to the last line.
func resolutionDetail(raw, fallback string) string {
	lines := strings.Split(raw, "\n")
	var kept []string
	for i := len(lines) - 1; i >= 0 && len(kept) < 12; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" && !nonActionableRuntimeLine(l) {
			kept = append([]string{l}, kept...)
		}
	}
	joined := strings.TrimSpace(strings.Join(kept, " "))
	if joined == "" {
		return fallback
	}
	const capLen = 600
	if len(joined) > capLen {
		joined = joined[len(joined)-capLen:]
	}
	return joined
}

// lastMeaningfulLine returns the last non-blank line of raw output (capped),
// which for pytest/uv failures is almost always the error summary.
func lastMeaningfulLine(raw string) string {
	const cap = 400
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if nonActionableRuntimeLine(l) {
				continue
			}
			if len(l) > cap {
				return l[:cap]
			}
			return l
		}
	}
	return ""
}

func nonActionableRuntimeLine(line string) bool {
	low := strings.ToLower(strings.TrimSpace(line))
	if low == "" {
		return true
	}
	if testProgressOnlyRuntimeLine(low) {
		return true
	}
	if uvProgressRuntimeLine(low) {
		return true
	}
	if strings.HasPrefix(low, "[notice]") || strings.HasPrefix(low, "notice:") {
		return true
	}
	if strings.Contains(low, "a new release of pip is available") {
		return true
	}
	if strings.Contains(low, "pip install --upgrade pip") &&
		(strings.Contains(low, "to update") || strings.Contains(low, "new release")) {
		return true
	}
	if strings.HasPrefix(low, "warning: you are using pip version") {
		return true
	}
	return false
}

// uvProgressRuntimeLine reports whether a (lowercased, trimmed) line is
// uv/pip DOWNLOAD/INSTALL PROGRESS — "Downloaded numpy", "Resolved 25 packages
// in 1.2s", " + numpy==1.19.2" — noise that must never be selected as an
// env-block detail (a killed sklearn source build once surfaced
// `env-blocked (unknown): Downloaded numpy` to the healer). Lines carrying
// error/failure/warning words are always kept, whatever their prefix.
func uvProgressRuntimeLine(low string) bool {
	if strings.Contains(low, "error") || strings.Contains(low, "failed") ||
		strings.Contains(low, "fatal") || strings.Contains(low, "warning") {
		return false
	}
	for _, prefix := range []string{
		"downloaded ", "downloading ", "resolved ", "installed ", "uninstalled ",
		"prepared ", "audited ", "built ", "building ", "updated ", "updating ",
		"cloned ", "cloning ", "checked out ", "creating virtual environment",
		"using cpython", "using python",
	} {
		if strings.HasPrefix(low, prefix) {
			return true
		}
	}
	// uv's install listing: " + numpy==1.19.2" / " - numpy==1.18.0".
	if (strings.HasPrefix(low, "+ ") || strings.HasPrefix(low, "- ")) && strings.Contains(low, "==") {
		return true
	}
	return false
}

func testProgressOnlyRuntimeLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return true
	}
	progressMarkers := 0
	for _, r := range line {
		switch {
		case r == ' ' || r == '\t' || r == '\r':
			continue
		case r >= '0' && r <= '9':
			continue
		case strings.ContainsRune(".fesxxuupprrdd-= []()%/", r):
			if strings.ContainsRune(".fesxxuupprrdd-", r) {
				progressMarkers++
			}
		default:
			return false
		}
	}
	return progressMarkers > 0
}

// StructuredSuite is one source-file's worth of cases.
type StructuredSuite struct {
	Name     string // file path (or class name when file unknown)
	File     string // absolute or repo-relative source path
	Duration time.Duration
	Cases    []*StructuredCase
}

// StructuredCase is one pytest invocation.
type StructuredCase struct {
	Name      string // local case name
	FullName  string // classname.name
	State     runtimev0.TestCaseState
	Duration  time.Duration
	File      string
	Line      int32
	Output    string // captured_output (populated only for FAILED/ERRORED)
	Truncated bool
	Failure   *StructuredFailure
}

// StructuredFailure carries the diagnostic detail for a failing case.
type StructuredFailure struct {
	Message string // one-line pytest summary
	Detail  string // traceback + captured output
	Kind    runtimev0.TestFailureKind
}

// ParsePytestJUnit parses the contents of pytest's --junitxml output
// into a StructuredTestRun. Empty raw, malformed XML, and "no tests
// collected" all surface as a StructuredTestRun with zero suites and
// zero cases — the caller decides whether that's an error.
//
// coverage is scraped separately (pytest-cov writes to stdout, not
// the XML); pass it through if available, 0 otherwise.
func ParsePytestJUnit(rawXML string, coverage float32) *StructuredTestRun {
	r := &StructuredTestRun{CoveragePct: coverage}
	if strings.TrimSpace(rawXML) == "" {
		return r
	}

	// Try `<testsuites>` first; fall back to a single top-level
	// `<testsuite>`. Both forms are valid pytest output depending on
	// pytest version + plugins.
	var outer junitTestsuites
	if err := xml.Unmarshal([]byte(rawXML), &outer); err != nil || len(outer.Suites) == 0 {
		var single junitTestsuite
		if err := xml.Unmarshal([]byte(rawXML), &single); err != nil {
			return r
		}
		outer.Suites = []junitTestsuite{single}
	}

	// Group cases by file. A test file maps to one StructuredSuite.
	// Cases without a `file` attribute (rare; very old pytest) fall
	// into a synthetic "<unknown>" suite.
	byFile := make(map[string]*StructuredSuite)
	suiteOrder := []string{} // preserves insertion order for stable output

	for _, ts := range outer.Suites {
		for _, tc := range ts.Cases {
			file := tc.File
			if file == "" {
				file = "<unknown>"
			}
			suite, ok := byFile[file]
			if !ok {
				suite = &StructuredSuite{Name: file, File: file}
				byFile[file] = suite
				suiteOrder = append(suiteOrder, file)
			}

			c := &StructuredCase{
				Name:     tc.Name,
				FullName: tc.ClassName + "." + tc.Name,
				Duration: time.Duration(tc.Time * float64(time.Second)),
				File:     file,
				Line:     int32(tc.Line),
			}

			switch {
			case tc.Failure != nil:
				c.State = runtimev0.TestCaseState_TEST_CASE_STATE_FAILED
				c.Failure = &StructuredFailure{
					Message: tc.Failure.Message,
					Detail:  joinFailureDetail(tc.Failure.Body, tc.SystemOut, tc.SystemErr),
					Kind:    classifyFailure(tc.Failure.Type, tc.Failure.Body),
				}
				c.Output = capOutput(c.Failure.Detail, &c.Truncated)
				if c.Truncated {
					r.truncatedCases++
				}
			case tc.Error != nil:
				c.State = runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED
				c.Failure = &StructuredFailure{
					Message: tc.Error.Message,
					Detail:  joinFailureDetail(tc.Error.Body, tc.SystemOut, tc.SystemErr),
					// `<error>` in JUnit pytest typically means
					// fixture/setup failure or exception during
					// collection — distinct from an assertion fail.
					Kind: runtimev0.TestFailureKind_TEST_FAILURE_KIND_SETUP,
				}
				c.Output = capOutput(c.Failure.Detail, &c.Truncated)
				if c.Truncated {
					r.truncatedCases++
				}
			case tc.Skipped != nil:
				c.State = runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED
				// Skip detail goes in TestFailure.skip_reason. We
				// surface it through the same struct because Mind/
				// CLI consumers want one place to look. Output
				// stays empty per the retention rule.
				c.Failure = &StructuredFailure{
					Message: tc.Skipped.Message,
				}
			default:
				c.State = runtimev0.TestCaseState_TEST_CASE_STATE_PASSED
				// Output stays empty for PASSED — the size-discipline
				// rule. Even if pytest captured stdout/stderr (for
				// debugging), we drop it here. Verbose mode would
				// re-include it; not modeled in this layer yet.
			}

			suite.Cases = append(suite.Cases, c)
			suite.Duration += c.Duration
		}
	}

	// Stable suite ordering — alphabetical by file path. Mirrors the
	// Go runner's deterministic-output rule.
	sort.Strings(suiteOrder)
	for _, file := range suiteOrder {
		// Sort cases inside each suite alphabetically too.
		s := byFile[file]
		sort.Slice(s.Cases, func(i, j int) bool {
			return s.Cases[i].Name < s.Cases[j].Name
		})
		r.Suites = append(r.Suites, s)
	}
	return r
}

// joinFailureDetail combines the `<failure>` body with any
// `<system-out>` / `<system-err>` pytest emitted. The body is
// usually the most useful (traceback + captured-during-failure
// output); system-out/err are runner-level captures.
func joinFailureDetail(body, sysOut, sysErr string) string {
	parts := []string{strings.TrimSpace(body)}
	if s := strings.TrimSpace(sysOut); s != "" {
		parts = append(parts, "--- captured stdout ---", s)
	}
	if s := strings.TrimSpace(sysErr); s != "" {
		parts = append(parts, "--- captured stderr ---", s)
	}
	return strings.Join(parts, "\n")
}

// capOutput truncates s at MaxCapturedOutputBytesPerCase and sets
// truncated=true if it fired. Same shape as the Go-runner's
// appendCapped helper.
func capOutput(s string, truncated *bool) string {
	if len(s) <= MaxCapturedOutputBytesPerCase {
		return s
	}
	*truncated = true
	return s[:MaxCapturedOutputBytesPerCase] + "\n[output truncated]\n"
}

// classifyFailure picks the FailureKind from the `type` attribute
// pytest emits ("AssertionError", "Failed", "TimeoutError", etc.)
// plus the body text as a fallback. Conservative: defaults to
// ASSERTION when unsure.
func classifyFailure(failureType, body string) runtimev0.TestFailureKind {
	t := strings.ToLower(failureType)
	switch {
	case strings.Contains(t, "timeout"):
		return runtimev0.TestFailureKind_TEST_FAILURE_KIND_TIMEOUT
	case strings.Contains(t, "syntaxerror"), strings.Contains(t, "importerror"):
		return runtimev0.TestFailureKind_TEST_FAILURE_KIND_BUILD_ERROR
	}
	lower := strings.ToLower(body)
	switch {
	case strings.Contains(lower, "timeout"):
		return runtimev0.TestFailureKind_TEST_FAILURE_KIND_TIMEOUT
	case strings.Contains(lower, "traceback") && !strings.Contains(lower, "assert"):
		return runtimev0.TestFailureKind_TEST_FAILURE_KIND_PANIC
	}
	return runtimev0.TestFailureKind_TEST_FAILURE_KIND_ASSERTION
}

// --- Conversion to protobuf TestResponse ---------------------------

// ToProtoResponse constructs the runtimev0.TestResponse with both
// the structured tree (preferred) AND legacy flat fields populated
// (back-compat for non-migrated consumers).
//
// runner is "pytest"; suiteName echoes TestRequest.suite. duration
// is the wall-clock for the whole run.
func (r *StructuredTestRun) ToProtoResponse(runner, suiteName string, duration time.Duration) *runtimev0.TestResponse {
	suites := make([]*runtimev0.TestSuite, 0, len(r.Suites))
	counts := &runtimev0.TestCounts{}
	legacyFailures := make([]string, 0)
	var legacyOutput strings.Builder

	for _, s := range r.Suites {
		ps, sCounts := s.toProto()
		suites = append(suites, ps)
		counts.Total += sCounts.Total
		counts.Passed += sCounts.Passed
		counts.Failed += sCounts.Failed
		counts.Skipped += sCounts.Skipped
		counts.Errored += sCounts.Errored

		for _, pc := range ps.GetCases() {
			if pc.GetState() == runtimev0.TestCaseState_TEST_CASE_STATE_FAILED ||
				pc.GetState() == runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED {
				legacyFailures = append(legacyFailures,
					fmt.Sprintf("FAIL %s::%s\n%s", s.File, pc.GetName(), pc.GetCapturedOutput()))
			}
		}
	}

	// Default-verbosity unittest: no per-case suites, only the aggregate
	// trailer. Use it so Counts are correct (a healthy all-skipped/all-passing
	// run isn't reported as zero-executed). Named FAIL:/ERROR: cases, when
	// present, already populated counts above; only fill the gaps.
	if counts.Total == 0 && r.Summary != nil {
		counts.Total = int32(r.Summary.Total)
		counts.Passed = int32(r.Summary.Passed)
		counts.Failed = int32(r.Summary.Failed)
		counts.Errored = int32(r.Summary.Errored)
		counts.Skipped = int32(r.Summary.Skipped)
	}

	// Run-level result.
	runResult := &runtimev0.TestRunResult{
		State:   runtimev0.TestRunResult_PASSED,
		Message: "all tests passed",
	}
	if counts.Failed > 0 {
		runResult.State = runtimev0.TestRunResult_FAILED
		runResult.Message = fmt.Sprintf("%d test(s) failed", counts.Failed)
		// Dependency-warning-as-error: every failure is an EXTERNAL
		// dependency's (Pending)DeprecationWarning escalated to an error by
		// the project's warning filters — a VERSION conflict, not a defect in
		// the patch. Tag it env-blocked so callers route to healing (pin the
		// dep) instead of remediating the patch. This classification is
		// PLUGIN knowledge (Python warning semantics + site-packages layout);
		// it used to live mirrored in the Mind brain, where message drift
		// silently broke it.
		if detail := r.dependencyWarningBlockDetail(); detail != "" {
			runResult.Message = fmt.Sprintf("env-blocked (dependency-warning): %s (%d test(s) failed)", detail, counts.Failed)
		}
	}
	if counts.Errored > 0 {
		runResult.State = runtimev0.TestRunResult_ERRORED
		runResult.Message = fmt.Sprintf("%d run-level error(s)", counts.Errored)
	}
	// Environment block: the run could not EXECUTE the tests (zero cases + a
	// non-zero exit — dep failed to install, project failed to import, ...). This
	// is NOT "all passed" (the default when counts are zero); it is ERRORED so a
	// caller distinguishes "couldn't run" from "ran and passed/failed" from the
	// STRUCTURE. The classified reason rides in the message ("env-blocked
	// (<reason>): <detail>") so the tooling inner loop can heal the plugin config
	// without re-parsing raw output.
	if r.EnvError != nil {
		runResult.State = runtimev0.TestRunResult_ERRORED
		runResult.Message = fmt.Sprintf("env-blocked (%s): %s", r.EnvError.Reason, r.EnvError.Detail)
	}
	// Materialized-but-interrupted: the environment is USABLE (runner launched)
	// but the run was budget-cut before any case completed. Report a healthy,
	// non-errored result carrying the marker prefix — a pre-warm/health probe
	// treats this as ready without demanding a full suite finish.
	if r.Materialized && r.EnvError == nil && counts.Total == 0 {
		runResult.State = runtimev0.TestRunResult_PASSED
		runResult.Message = EnvMaterializedMessagePrefix + "the test runner launched and began executing before the probe budget elapsed; the environment can run the project's tests"
	}

	legacyState := runtimev0.TestStatus_SUCCESS
	if counts.Failed > 0 || counts.Errored > 0 || r.EnvError != nil {
		legacyState = runtimev0.TestStatus_ERROR
	}

	output := legacyOutput.String()
	if strings.TrimSpace(output) == "" && r.EnvError != nil {
		output = r.RawOutput
	}
	resp := &runtimev0.TestResponse{
		Run: &runtimev0.TestRun{
			Runner:    runner,
			SuiteName: suiteName,
			Duration:  durationpb.New(duration),
		},
		Result: runResult,
		Counts: counts,
		Suites: suites,
		Truncation: &runtimev0.TestTruncation{
			Happened:        r.truncatedCases > 0,
			MaxPerCaseBytes: int32(MaxCapturedOutputBytesPerCase),
			TruncatedCases:  r.truncatedCases,
		},

		Status:       &runtimev0.TestStatus{State: legacyState, Message: runResult.Message},
		Output:       output,
		TestsRun:     counts.Total,
		TestsPassed:  counts.Passed,
		TestsFailed:  counts.Failed,
		TestsSkipped: counts.Skipped,
		CoveragePct:  r.CoveragePct,
		Failures:     legacyFailures,
	}

	if r.CoveragePct > 0 {
		resp.Coverage = &runtimev0.TestCoverage{
			TotalPct:          r.CoveragePct,
			RawArtifactFormat: "pytest-cov",
		}
	}
	return resp
}

func (s *StructuredSuite) toProto() (*runtimev0.TestSuite, *runtimev0.TestCounts) {
	ps := &runtimev0.TestSuite{
		Name:     s.Name,
		File:     s.File,
		Duration: durationpb.New(s.Duration),
	}
	suiteCounts := &runtimev0.TestCounts{}

	for _, c := range s.Cases {
		pc := &runtimev0.TestCase{
			Name:     c.Name,
			FullName: c.FullName,
			State:    c.State,
			Duration: durationpb.New(c.Duration),
		}
		if c.File != "" || c.Line > 0 {
			pc.Location = &runtimev0.TestLocation{
				File: c.File,
				Line: c.Line,
			}
		}
		if c.State == runtimev0.TestCaseState_TEST_CASE_STATE_FAILED ||
			c.State == runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED {
			pc.CapturedOutput = c.Output
		}
		if c.Failure != nil {
			pc.Failure = &runtimev0.TestFailure{
				Message: c.Failure.Message,
				Detail:  c.Failure.Detail,
				Kind:    c.Failure.Kind,
			}
			if c.State == runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED {
				pc.Failure.SkipReason = c.Failure.Message
			}
		}

		ps.Cases = append(ps.Cases, pc)
		suiteCounts.Total++
		switch c.State {
		case runtimev0.TestCaseState_TEST_CASE_STATE_PASSED:
			suiteCounts.Passed++
		case runtimev0.TestCaseState_TEST_CASE_STATE_FAILED:
			suiteCounts.Failed++
		case runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED:
			suiteCounts.Skipped++
		case runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED:
			suiteCounts.Errored++
		}
	}
	ps.Counts = suiteCounts
	return ps, suiteCounts
}

// LegacyTestSummary returns the flat shape callers that haven't
// migrated still consume. Computed from the structured tree —
// single source of truth.
func (r *StructuredTestRun) LegacyTestSummary() *TestSummary {
	s := &TestSummary{Coverage: r.CoveragePct}
	for _, suite := range r.Suites {
		for _, c := range suite.Cases {
			s.Run++
			switch c.State {
			case runtimev0.TestCaseState_TEST_CASE_STATE_PASSED:
				s.Passed++
			case runtimev0.TestCaseState_TEST_CASE_STATE_FAILED:
				s.Failed++
				s.Failures = append(s.Failures, fmt.Sprintf("FAIL %s::%s\n%s",
					suite.File, c.Name, c.Output))
			case runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED:
				s.Skipped++
			case runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED:
				s.Failed++ // legacy lumps errored under failed
				s.Failures = append(s.Failures, fmt.Sprintf("ERROR %s::%s\n%s",
					suite.File, c.Name, c.Output))
			}
		}
	}
	return s
}

// dependencyWarningBlockDetail reports a one-line detail when the run's
// failures are caused by an external dependency's deprecation warning being
// treated as an error (warning filters escalate; the warning originates under
// site-packages — i.e. NOT the project's own code). Empty when the failures
// look like real test failures.
func (r *StructuredTestRun) dependencyWarningBlockDetail() string {
	var failureText strings.Builder
	for _, suite := range r.Suites {
		for _, c := range suite.Cases {
			if c.State != runtimev0.TestCaseState_TEST_CASE_STATE_FAILED &&
				c.State != runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED {
				continue
			}
			if c.Failure != nil {
				failureText.WriteString(c.Failure.Message)
				failureText.WriteString("\n")
			}
			failureText.WriteString(c.Output)
			failureText.WriteString("\n")
		}
	}
	text := failureText.String()
	low := strings.ToLower(text)
	if !strings.Contains(low, "deprecationwarning") && !strings.Contains(low, "pendingdeprecationwarning") {
		return ""
	}
	if !strings.Contains(low, "site-packages/") && !strings.Contains(low, "site-packages\\") {
		return ""
	}
	// First line naming the warning is the actionable detail.
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "deprecationwarning") {
			return "external dependency deprecation warning treated as error: " + strings.TrimSpace(line)
		}
	}
	return "external dependency deprecation warning treated as error"
}
