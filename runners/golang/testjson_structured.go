package golang

import (
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// MaxCapturedOutputBytesPerCase caps the per-case captured_output
// stored in the structured TestResponse. Above this we truncate +
// surface the count via TestTruncation. Tuned to "enough for a
// stack trace and a hundred lines of context" — most failures fit
// in a few KB; multi-MB stdout is a tool misconfiguration.
const MaxCapturedOutputBytesPerCase = 32 * 1024 // 32 KiB

// StructuredTestRun is the SOTA representation built by walking
// `go test -json` events. Holds the full hierarchy + per-case
// captured output. Convertible to runtimev0.TestResponse via
// ToProtoResponse, OR to the legacy flat *TestSummary via Legacy
// for callers that haven't migrated yet.
//
// Build by calling ParseTestJSONStructured; access via the methods.
type StructuredTestRun struct {
	// Started captures wall-clock start (the first event). Used to
	// derive Run.duration when the run completes.
	Started time.Time

	// Suites — one per Go package, indexed by import path. The
	// hierarchy is flat for Go (Go has no nested packages-in-packages
	// grouping that go test surfaces; the proto's recursive shape
	// supports nested for jest/pytest, unused here).
	Suites map[string]*structuredSuite

	// Coverage — populated when `go test -cover` is run; we observe
	// the coverage line in package-output.
	CoveragePct float32

	// truncatedCases tracks how many cases had captured_output cut.
	truncatedCases int32
}

// structuredSuite is the in-progress representation of a single
// Go package's test results. Has a strongly-typed `cases` map keyed
// by case name for O(1) lookup as events stream in.
type structuredSuite struct {
	Name      string
	cases     map[string]*structuredCase
	startedAt time.Time
	finished  bool
	// elapsed is the package-level "X passed, X failed, X skipped (1.234s)"
	// duration — set when the package's terminal action fires.
	elapsed time.Duration
	// output is captured BEFORE any per-case event arrives (build
	// errors, package-level setup logs).
	output strings.Builder
	// errored is true when the package itself failed to build/run,
	// independent of individual case failures.
	errored bool
	// errorReason holds the build/setup error message when errored
	// is true.
	errorReason string
}

// structuredCase is one Go test invocation. Captures the per-case
// output up to the cap.
type structuredCase struct {
	Name      string
	state     runtimev0.TestCaseState
	startedAt time.Time
	elapsed   time.Duration
	output    strings.Builder
	truncated bool
}

// ParseTestJSONStructured walks every `go test -json` event in raw
// and returns the structured representation. Equivalent in coverage
// to ParseTestJSON but preserves the full tree.
//
// Order of operations matters: we observe `run` before any other
// event for a case to set startedAt; the terminal action sets state
// + elapsed.
func ParseTestJSONStructured(raw string) *StructuredTestRun {
	r := &StructuredTestRun{
		Suites:  make(map[string]*structuredSuite),
		Started: time.Now(),
	}

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev TestEvent
		if err := unmarshalEvent(line, &ev); err != nil {
			continue
		}
		pkg := ev.Package
		if pkg == "" {
			pkg = ev.ImportPath
		}
		if pkg == "" {
			continue
		}

		suite := r.suite(pkg)

		switch ev.Action {
		case "start":
			suite.startedAt = time.Now()
		case "run":
			if ev.Test == "" {
				continue
			}
			c := suite.caseFor(ev.Test)
			c.startedAt = time.Now()
		case "pass":
			r.applyTerminal(suite, ev, runtimev0.TestCaseState_TEST_CASE_STATE_PASSED)
		case "fail":
			if ev.Test == "" {
				// Package-level fail event. `go test -json` emits one
				// of these at the end of every package that contains
				// at least one failure — INCLUDING packages where
				// individual tests have already reported their own
				// fails. Treating every package-level fail as a
				// separate suite-level error would double-count.
				//
				// Rule: only mark the suite as ERRORED if no
				// individual case has failed yet AND it isn't already
				// flagged via build-fail. That covers genuine setup-
				// or-fixture failures while letting normal "package
				// summary fail" events be no-ops.
				if !suite.errored && !hasFailedCase(suite) {
					suite.errored = true
					if suite.errorReason == "" {
						suite.errorReason = strings.TrimSpace(suite.output.String())
					}
				}
				suite.finished = true
				suite.elapsed = time.Duration(ev.Elapsed * float64(time.Second))
				continue
			}
			r.applyTerminal(suite, ev, runtimev0.TestCaseState_TEST_CASE_STATE_FAILED)
		case "skip":
			r.applyTerminal(suite, ev, runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED)
		case "build-output":
			suite.output.WriteString(ev.Output)
		case "build-fail":
			suite.errored = true
			suite.errorReason = strings.TrimSpace(suite.output.String())
			suite.finished = true
		case "output":
			// Coverage scrape — same regex as the legacy parser.
			if m := coverageRe.FindStringSubmatch(ev.Output); len(m) > 1 {
				var pct float64
				_, _ = fmt.Sscanf(m[1], "%f", &pct)
				if float32(pct) > r.CoveragePct {
					r.CoveragePct = float32(pct)
				}
			}
			if ev.Test != "" {
				c := suite.caseFor(ev.Test)
				appendCapped(&c.output, &c.truncated, ev.Output)
				if c.truncated {
					// Bump the run-level truncation count exactly once
					// per case — set when truncated transitions to true.
				}
			} else {
				// Package-level output (setup logs, panics during
				// init). Append to suite output unbounded — Go's own
				// output streams have natural ceilings here.
				suite.output.WriteString(ev.Output)
			}
		}
	}

	// Post-walk: count truncated cases.
	for _, suite := range r.Suites {
		for _, c := range suite.cases {
			if c.truncated {
				r.truncatedCases++
			}
		}
	}
	return r
}

// applyTerminal sets a case's terminal state + elapsed time. Shared
// for pass/fail/skip; the only difference is the resulting state.
func (r *StructuredTestRun) applyTerminal(suite *structuredSuite, ev TestEvent, state runtimev0.TestCaseState) {
	if ev.Test == "" {
		// Package-level terminal — set suite duration; case states
		// already set by their own per-case events.
		suite.finished = true
		suite.elapsed = time.Duration(ev.Elapsed * float64(time.Second))
		return
	}
	c := suite.caseFor(ev.Test)
	c.state = state
	c.elapsed = time.Duration(ev.Elapsed * float64(time.Second))
}

// suite returns the suite for pkg, creating one if absent.
func (r *StructuredTestRun) suite(pkg string) *structuredSuite {
	s, ok := r.Suites[pkg]
	if !ok {
		s = &structuredSuite{
			Name:  pkg,
			cases: make(map[string]*structuredCase),
		}
		r.Suites[pkg] = s
	}
	return s
}

// hasFailedCase returns true when at least one case in the suite has
// already terminated as FAILED. Used to distinguish "package summary
// fail" (no-op) from "package-level error" (mark suite errored).
func hasFailedCase(s *structuredSuite) bool {
	for _, c := range s.cases {
		if c.state == runtimev0.TestCaseState_TEST_CASE_STATE_FAILED {
			return true
		}
	}
	return false
}

// caseFor returns the case in suite, creating one if absent.
func (s *structuredSuite) caseFor(name string) *structuredCase {
	c, ok := s.cases[name]
	if !ok {
		c = &structuredCase{Name: name, state: runtimev0.TestCaseState_TEST_CASE_STATE_UNSPECIFIED}
		s.cases[name] = c
	}
	return c
}

// appendCapped appends s to dst up to MaxCapturedOutputBytesPerCase;
// once the cap is hit, sets truncated=true and stops growing.
func appendCapped(dst *strings.Builder, truncated *bool, s string) {
	if *truncated {
		return
	}
	remaining := MaxCapturedOutputBytesPerCase - dst.Len()
	if remaining <= 0 {
		*truncated = true
		return
	}
	if len(s) <= remaining {
		dst.WriteString(s)
		return
	}
	dst.WriteString(s[:remaining])
	dst.WriteString("\n[output truncated]\n")
	*truncated = true
}

// unmarshalEvent is a tiny indirection so tests can stub event
// decoding without exposing encoding/json's internals. Production
// path is a one-line wrapper.
func unmarshalEvent(line string, ev *TestEvent) error {
	return jsonUnmarshal([]byte(line), ev)
}

// --- Conversion to protobuf TestResponse ---------------------------

// ToProtoResponse constructs the runtimev0.TestResponse with BOTH the
// structured tree (preferred) AND the legacy flat fields populated
// (for backward compat). Single source of truth: every count/value
// in the legacy fields is computed from the structured tree.
//
// runner is the runner identifier ("go-test"); suiteName echoes
// TestRequest.suite. duration is the wall-clock for the whole run
// (caller measures it).
func (r *StructuredTestRun) ToProtoResponse(runner, suiteName string, duration time.Duration) *runtimev0.TestResponse {
	suites := make([]*runtimev0.TestSuite, 0, len(r.Suites))
	counts := &runtimev0.TestCounts{}
	legacyFailures := make([]string, 0)
	var legacyOutput strings.Builder

	// Stable ordering: alphabetical by suite name. Deterministic
	// output makes diffs across runs reviewable.
	names := make([]string, 0, len(r.Suites))
	for name := range r.Suites {
		names = append(names, name)
	}
	sortStrings(names)

	for _, name := range names {
		s := r.Suites[name]
		ps, sCounts := s.toProto()
		suites = append(suites, ps)
		counts.Total += sCounts.Total
		counts.Passed += sCounts.Passed
		counts.Failed += sCounts.Failed
		counts.Skipped += sCounts.Skipped
		counts.Errored += sCounts.Errored

		// Legacy compat — list of "FAIL pkg/Test" strings.
		for _, pc := range ps.GetCases() {
			if pc.GetState() == runtimev0.TestCaseState_TEST_CASE_STATE_FAILED ||
				pc.GetState() == runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED {
				legacyFailures = append(legacyFailures,
					fmt.Sprintf("FAIL %s/%s\n%s", name, pc.GetName(), pc.GetCapturedOutput()))
			}
		}
		if s.errored {
			legacyFailures = append(legacyFailures, fmt.Sprintf("FAIL %s\n%s", name, s.errorReason))
		}
		// Legacy output: best-effort concatenation of suite-level output.
		legacyOutput.WriteString(s.output.String())
	}

	// Run-level result: PASSED iff zero failed AND zero errored.
	runResult := &runtimev0.TestRunResult{
		State:   runtimev0.TestRunResult_PASSED,
		Message: "all tests passed",
	}
	if counts.Failed > 0 {
		runResult.State = runtimev0.TestRunResult_FAILED
		runResult.Message = fmt.Sprintf("%d test(s) failed", counts.Failed)
	}
	if counts.Errored > 0 {
		runResult.State = runtimev0.TestRunResult_ERRORED
		runResult.Message = fmt.Sprintf("%d run-level error(s)", counts.Errored)
	}

	// Legacy status: SUCCESS unless we have failures.
	legacyState := runtimev0.TestStatus_SUCCESS
	if counts.Failed > 0 || counts.Errored > 0 {
		legacyState = runtimev0.TestStatus_ERROR
	}

	resp := &runtimev0.TestResponse{
		// Structured (preferred):
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

		// Legacy flat (deprecated; populated for transition):
		Status:       &runtimev0.TestStatus{State: legacyState, Message: runResult.Message},
		Output:       legacyOutput.String(),
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
			RawArtifactFormat: "go-cover",
		}
	}

	return resp
}

// toProto converts a single suite into its protobuf form, also
// returning the suite's counts for run-level aggregation.
func (s *structuredSuite) toProto() (*runtimev0.TestSuite, *runtimev0.TestCounts) {
	ps := &runtimev0.TestSuite{
		Name:     s.Name,
		Duration: durationpb.New(s.elapsed),
	}
	suiteCounts := &runtimev0.TestCounts{}

	// Stable ordering: alphabetical by case name.
	names := make([]string, 0, len(s.cases))
	for name := range s.cases {
		names = append(names, name)
	}
	sortStrings(names)

	for _, name := range names {
		c := s.cases[name]
		pc := &runtimev0.TestCase{
			Name:     c.Name,
			FullName: s.Name + "." + c.Name,
			State:    c.state,
			Duration: durationpb.New(c.elapsed),
		}

		// captured_output retention rule: populate ONLY for
		// FAILED/ERRORED. PASSED + SKIPPED get empty (unless caller
		// passes verbose — we don't model that here yet; verbose
		// support belongs at the agent level).
		if c.state == runtimev0.TestCaseState_TEST_CASE_STATE_FAILED ||
			c.state == runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED {
			pc.CapturedOutput = c.output.String()
			pc.Failure = &runtimev0.TestFailure{
				Message: extractFailureMessage(c.output.String()),
				Detail:  c.output.String(),
				Kind:    extractFailureKind(c.output.String()),
			}
		}

		ps.Cases = append(ps.Cases, pc)
		suiteCounts.Total++
		switch c.state {
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

	if s.errored {
		// Build/setup error attributed to this suite. Surface via
		// errored count plus a synthetic case carrying the error.
		suiteCounts.Errored++
		suiteCounts.Total++
		ps.Cases = append(ps.Cases, &runtimev0.TestCase{
			Name:     "<package>",
			FullName: s.Name,
			State:    runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED,
			Failure: &runtimev0.TestFailure{
				Message: "package failed to build or initialize",
				Detail:  s.errorReason,
				Kind:    runtimev0.TestFailureKind_TEST_FAILURE_KIND_BUILD_ERROR,
			},
			CapturedOutput: s.errorReason,
		})
	}

	ps.Counts = suiteCounts
	return ps, suiteCounts
}

// extractFailureMessage takes the per-case output and tries to pick
// out the assertion message. Heuristic: the line containing
// "Error:" (testify's convention) or the first non-blank line.
func extractFailureMessage(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "Error:") || strings.HasPrefix(trim, "Error Trace:") {
			return trim
		}
	}
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim != "" && !strings.HasPrefix(trim, "===") && !strings.HasPrefix(trim, "---") {
			return trim
		}
	}
	return ""
}

// extractFailureKind classifies the failure based on output markers.
// Conservative — when unsure, return ASSERTION (the common case).
func extractFailureKind(output string) runtimev0.TestFailureKind {
	if strings.Contains(output, "panic:") || strings.Contains(output, "goroutine ") {
		return runtimev0.TestFailureKind_TEST_FAILURE_KIND_PANIC
	}
	if strings.Contains(output, "test timed out") || strings.Contains(output, "context deadline exceeded") {
		return runtimev0.TestFailureKind_TEST_FAILURE_KIND_TIMEOUT
	}
	return runtimev0.TestFailureKind_TEST_FAILURE_KIND_ASSERTION
}

// sortStrings is a tiny shim — using sort.Strings here pulls in
// import gymnastics for what's a one-line need. Inlined.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// jsonUnmarshal is the test-stubbable JSON entrypoint. In production
// it's encoding/json.Unmarshal. Hidden behind a var so tests can
// substitute (none today; reserved).
var jsonUnmarshal = unmarshalJSON
