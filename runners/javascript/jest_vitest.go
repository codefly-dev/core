package javascript

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// jestVitestEnvelope is the shape vitest and jest both emit when
// invoked with `--reporter=json` (vitest) or `--json` (jest).
//
// Field-for-field compatible — vitest copied jest's reporter
// schema. The only practical differences are:
//   - vitest may produce slightly different `status` strings; we
//     normalize via mapStatus.
//   - failureMessages content varies (vitest is cleaner); we treat
//     them uniformly.
type jestVitestEnvelope struct {
	NumTotalTestSuites  int                    `json:"numTotalTestSuites"`
	NumPassedTestSuites int                    `json:"numPassedTestSuites"`
	NumFailedTestSuites int                    `json:"numFailedTestSuites"`
	NumTotalTests       int                    `json:"numTotalTests"`
	NumPassedTests      int                    `json:"numPassedTests"`
	NumFailedTests      int                    `json:"numFailedTests"`
	NumPendingTests     int                    `json:"numPendingTests"`
	TestResults         []jestVitestSuite      `json:"testResults"`
	CoverageMap         map[string]interface{} `json:"coverageMap,omitempty"`
}

// jestVitestSuite is one source file's worth of tests.
type jestVitestSuite struct {
	Name              string                `json:"name"` // absolute file path
	Status            string                `json:"status"` // "passed" | "failed"
	StartTime         int64                 `json:"startTime"` // ms epoch
	EndTime           int64                 `json:"endTime"`
	AssertionResults  []jestVitestAssertion `json:"assertionResults"`
	// FailureMessage is the file-level "couldn't even load" reason
	// when status="failed" but no individual tests are present
	// (e.g. import error in the test file). Surface as suite-level
	// errored.
	FailureMessage string `json:"failureMessage"`
}

// jestVitestAssertion is one test invocation.
type jestVitestAssertion struct {
	AncestorTitles []string `json:"ancestorTitles"` // describe() chain
	Title          string   `json:"title"`           // the test() name
	FullName       string   `json:"fullName"`        // "describe > test"
	Status         string   `json:"status"`          // "passed" | "failed" | "skipped" | "pending" | "todo"
	Duration       int64    `json:"duration"`        // ms (jest); vitest may emit float
	FailureMessages []string `json:"failureMessages"`
	Location       *jestVitestLocation `json:"location,omitempty"`
}

// jestVitestLocation carries file:line for the assertion. jest emits
// it when --testLocationInResults is on; vitest emits it by default
// in newer versions.
type jestVitestLocation struct {
	Line   int32 `json:"line"`
	Column int32 `json:"column"`
}

// ParseJestVitestJSON parses the JSON envelope vitest/jest produce
// and returns a StructuredTestRun. Empty raw, malformed JSON, or
// envelope-with-no-suites all surface as an empty run — the caller
// uses the runErr to decide if the runner crashed.
//
// coverage is scraped separately from the runner's terminal output
// (the JSON's `coverageMap` carries per-file marks but not the
// summary percentage); pass coverage as 0 when not measured.
func ParseJestVitestJSON(rawJSON string, coverage float32) *StructuredTestRun {
	r := &StructuredTestRun{CoveragePct: coverage}
	if strings.TrimSpace(rawJSON) == "" {
		return r
	}

	var env jestVitestEnvelope
	if err := json.Unmarshal([]byte(rawJSON), &env); err != nil {
		return r
	}

	for _, suite := range env.TestResults {
		ss := &StructuredSuite{
			Name: suite.Name,
			File: suite.Name,
			Duration: time.Duration(
				(suite.EndTime - suite.StartTime) * int64(time.Millisecond),
			),
		}

		// Suite-level "couldn't load" — vitest/jest set status=failed
		// at suite level with no assertions when the file failed to
		// import. Surface as a synthetic <file> case with kind=BUILD_ERROR.
		if len(suite.AssertionResults) == 0 && suite.Status == "failed" && suite.FailureMessage != "" {
			detail := suite.FailureMessage
			truncated := false
			capped := capOutput(detail, &truncated)
			if truncated {
				r.truncatedCases++
			}
			ss.Cases = append(ss.Cases, &StructuredCase{
				Name:     "<file>",
				FullName: suite.Name,
				State:    runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED,
				File:     suite.Name,
				Output:   capped,
				Failure: &StructuredFailure{
					Message: extractFirstLine(detail),
					Detail:  detail,
					Kind:    runtimev0.TestFailureKind_TEST_FAILURE_KIND_BUILD_ERROR,
				},
			})
			r.Suites = append(r.Suites, ss)
			continue
		}

		for _, a := range suite.AssertionResults {
			c := &StructuredCase{
				Name:     a.Title,
				FullName: assertionFullName(a),
				State:    mapJestVitestStatus(a.Status),
				Duration: time.Duration(a.Duration) * time.Millisecond,
				File:     suite.Name,
			}
			if a.Location != nil {
				c.Line = a.Location.Line
			}

			if c.State == runtimev0.TestCaseState_TEST_CASE_STATE_FAILED && len(a.FailureMessages) > 0 {
				detail := strings.Join(a.FailureMessages, "\n\n")
				truncated := false
				capped := capOutput(detail, &truncated)
				if truncated {
					r.truncatedCases++
				}
				c.Output = capped
				c.Truncated = truncated
				c.Failure = &StructuredFailure{
					Message: extractFirstLine(detail),
					Detail:  detail,
					Kind:    classifyJestFailure(detail),
				}
			}

			ss.Cases = append(ss.Cases, c)
		}
		r.Suites = append(r.Suites, ss)
	}
	return r
}

// mapJestVitestStatus normalizes the runner's status string to the
// platform's TestCaseState. jest distinguishes "skipped", "pending",
// and "todo"; we collapse all three to SKIPPED — the platform's
// semantics don't model the difference and consumers don't need it.
func mapJestVitestStatus(s string) runtimev0.TestCaseState {
	switch s {
	case "passed":
		return runtimev0.TestCaseState_TEST_CASE_STATE_PASSED
	case "failed":
		return runtimev0.TestCaseState_TEST_CASE_STATE_FAILED
	case "skipped", "pending", "todo", "disabled":
		return runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED
	default:
		// Unknown status — treat as ERRORED so it surfaces in the
		// aggregate counts rather than getting silently dropped.
		return runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED
	}
}

// assertionFullName prefers the runner's `fullName` when present,
// falling back to `describe > test` from the ancestor chain.
func assertionFullName(a jestVitestAssertion) string {
	if a.FullName != "" {
		return a.FullName
	}
	if len(a.AncestorTitles) == 0 {
		return a.Title
	}
	return strings.Join(append(a.AncestorTitles, a.Title), " > ")
}

// classifyJestFailure picks the FailureKind from the failure message
// content. JS test runners don't have a clean "type" field like
// pytest's JUnit XML, so we scan the text.
func classifyJestFailure(detail string) runtimev0.TestFailureKind {
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "exceeded timeout") || strings.Contains(lower, "timed out"):
		return runtimev0.TestFailureKind_TEST_FAILURE_KIND_TIMEOUT
	case strings.Contains(lower, "syntaxerror"), strings.Contains(lower, "module not found"),
		strings.Contains(lower, "cannot find module"):
		return runtimev0.TestFailureKind_TEST_FAILURE_KIND_BUILD_ERROR
	case strings.Contains(lower, "uncaught") && !strings.Contains(lower, "expect"):
		return runtimev0.TestFailureKind_TEST_FAILURE_KIND_PANIC
	}
	return runtimev0.TestFailureKind_TEST_FAILURE_KIND_ASSERTION
}

// extractFirstLine returns the first non-blank line of s. Used for
// the human-readable failure.message; the full detail lives in
// failure.detail.
func extractFirstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// capOutput truncates s at MaxCapturedOutputBytesPerCase, returning
// the (possibly truncated) string and setting truncated=true when
// the cap fired.
func capOutput(s string, truncated *bool) string {
	if len(s) <= MaxCapturedOutputBytesPerCase {
		return s
	}
	*truncated = true
	return s[:MaxCapturedOutputBytesPerCase] + "\n[output truncated]\n"
}

// silence unused-fmt warning when only used inside another file's path.
var _ = fmt.Sprintf
