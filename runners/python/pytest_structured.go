package python

import (
	"encoding/xml"
	"fmt"
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
	XMLName xml.Name        `xml:"testsuites"`
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
	XMLName    xml.Name      `xml:"testcase"`
	ClassName  string        `xml:"classname,attr"`
	Name       string        `xml:"name,attr"`
	Time       float64       `xml:"time,attr"`
	File       string        `xml:"file,attr"`
	Line       int           `xml:"line,attr"`
	Failure    *junitDetail  `xml:"failure,omitempty"`
	Error      *junitDetail  `xml:"error,omitempty"`
	Skipped    *junitDetail  `xml:"skipped,omitempty"`
	SystemOut  string        `xml:"system-out,omitempty"`
	SystemErr  string        `xml:"system-err,omitempty"`
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

	// truncatedCases tracks how many cases had their captured_output
	// trimmed by the per-case cap.
	truncatedCases int32
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

	// Run-level result.
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

	legacyState := runtimev0.TestStatus_SUCCESS
	if counts.Failed > 0 || counts.Errored > 0 {
		legacyState = runtimev0.TestStatus_ERROR
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
