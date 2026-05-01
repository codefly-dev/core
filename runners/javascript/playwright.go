package javascript

import (
	"encoding/json"
	"strings"
	"time"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// playwrightEnvelope is the JSON shape Playwright emits with
// `--reporter=json`. Different from jest/vitest: Playwright has its
// own nested suites + per-test results-as-array (one entry per
// retry attempt).
type playwrightEnvelope struct {
	Stats playwrightStats `json:"stats"`
	// Suites is a recursive tree: top-level Suites mirror the
	// playwright config's `projects`; child suites are file-scoped;
	// further children are describe-blocks.
	Suites []playwrightSuite `json:"suites"`
	// Errors at the top level surface non-test failures (config
	// errors, global setup crashes) — distinct from per-test fails.
	Errors []playwrightError `json:"errors,omitempty"`
}

type playwrightStats struct {
	StartTime string `json:"startTime"`
	Duration  int64  `json:"duration"` // ms
	Expected  int    `json:"expected"`
	Skipped   int    `json:"skipped"`
	Unexpected int   `json:"unexpected"`
	Flaky      int   `json:"flaky"`
}

type playwrightSuite struct {
	Title      string            `json:"title"`
	File       string            `json:"file"`
	Line       int32             `json:"line,omitempty"`
	Suites     []playwrightSuite `json:"suites,omitempty"`
	Specs      []playwrightSpec  `json:"specs,omitempty"`
}

// playwrightSpec is one `test()` declaration. Each spec carries one
// or more `tests` (one per project × retry combination).
type playwrightSpec struct {
	Title string           `json:"title"`
	File  string           `json:"file"`
	Line  int32            `json:"line"`
	Tests []playwrightTest `json:"tests"`
}

// playwrightTest is one project-or-retry attempt of a spec. The
// `results` array contains the timeline of attempts — `expectedStatus`
// is what playwright expected; `status` is what actually happened.
type playwrightTest struct {
	ProjectName    string             `json:"projectName"`
	ExpectedStatus string             `json:"expectedStatus"`
	Status         string             `json:"status"` // overall outcome of all retries: "expected"|"unexpected"|"flaky"|"skipped"
	Results        []playwrightResult `json:"results"`
}

// playwrightResult is one attempt's outcome.
type playwrightResult struct {
	Retry    int32              `json:"retry"`    // 0-indexed; 0 = first try
	Status   string             `json:"status"`   // "passed"|"failed"|"timedOut"|"skipped"
	Duration int64              `json:"duration"` // ms
	Error    *playwrightError   `json:"error,omitempty"`
	Stdout   []playwrightStream `json:"stdout,omitempty"`
	Stderr   []playwrightStream `json:"stderr,omitempty"`
}

type playwrightError struct {
	Message  string `json:"message"`
	Stack    string `json:"stack"`
	Location *struct {
		File   string `json:"file"`
		Line   int32  `json:"line"`
		Column int32  `json:"column"`
	} `json:"location,omitempty"`
}

type playwrightStream struct {
	Text string `json:"text"`
}

// ParsePlaywrightJSON parses Playwright's `--reporter=json` output
// and returns a StructuredTestRun. Coverage is not modeled —
// Playwright doesn't emit coverage in its standard JSON; users
// running coverage typically wrap with c8 or v8-coverage which
// produce separate artifacts.
func ParsePlaywrightJSON(rawJSON string) *StructuredTestRun {
	r := &StructuredTestRun{}
	if strings.TrimSpace(rawJSON) == "" {
		return r
	}

	var env playwrightEnvelope
	if err := json.Unmarshal([]byte(rawJSON), &env); err != nil {
		return r
	}

	// Group cases by source file. Walk the suite tree; every spec
	// lives inside a file-scoped suite.
	byFile := make(map[string]*StructuredSuite)
	for _, s := range env.Suites {
		walkPlaywrightSuite(s, byFile, &r.truncatedCases)
	}

	// Stable ordering — alphabetical by file. (ToProtoResponse
	// re-sorts to be defensive.)
	for _, suite := range byFile {
		r.Suites = append(r.Suites, suite)
	}
	return r
}

// walkPlaywrightSuite descends the suite tree, accumulating cases
// into the byFile map. Recursion handles nested describe blocks.
func walkPlaywrightSuite(s playwrightSuite, byFile map[string]*StructuredSuite, truncatedCases *int32) {
	for _, sub := range s.Suites {
		walkPlaywrightSuite(sub, byFile, truncatedCases)
	}
	for _, spec := range s.Specs {
		file := spec.File
		if file == "" {
			file = s.File
		}
		if file == "" {
			file = "<unknown>"
		}
		suite, ok := byFile[file]
		if !ok {
			suite = &StructuredSuite{Name: file, File: file}
			byFile[file] = suite
		}

		for _, t := range spec.Tests {
			c := buildPlaywrightCase(spec, t, file, truncatedCases)
			suite.Cases = append(suite.Cases, c)
			suite.Duration += c.Duration
		}
	}
}

// buildPlaywrightCase constructs the StructuredCase for one project-
// or-retry test. Retries are surfaced via Retries[].
func buildPlaywrightCase(spec playwrightSpec, t playwrightTest, file string, truncatedCases *int32) *StructuredCase {
	c := &StructuredCase{
		Name:     spec.Title,
		FullName: spec.Title + " (" + t.ProjectName + ")",
		State:    mapPlaywrightOutcome(t.Status),
		File:     file,
		Line:     spec.Line,
	}

	// Use the LAST result as the case's terminal — that's the one
	// that determined the case's outcome. Earlier results, if any,
	// are retry attempts.
	if len(t.Results) == 0 {
		return c
	}
	final := t.Results[len(t.Results)-1]
	c.Duration = time.Duration(final.Duration) * time.Millisecond

	if c.State == runtimev0.TestCaseState_TEST_CASE_STATE_FAILED ||
		c.State == runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED {
		detail := buildPlaywrightDetail(final)
		truncated := false
		capped := capOutput(detail, &truncated)
		if truncated {
			*truncatedCases++
		}
		c.Output = capped
		c.Truncated = truncated
		c.Failure = &StructuredFailure{
			Message: extractFirstLine(detail),
			Detail:  detail,
			Kind:    classifyPlaywrightStatus(final.Status),
		}
	}

	// Per-retry detail: every result EXCEPT the final.
	if len(t.Results) > 1 {
		for _, res := range t.Results[:len(t.Results)-1] {
			r := &StructuredRetry{
				Attempt:  res.Retry + 1,
				State:    mapPlaywrightResultStatus(res.Status),
				Duration: time.Duration(res.Duration) * time.Millisecond,
			}
			if r.State == runtimev0.TestCaseState_TEST_CASE_STATE_FAILED ||
				r.State == runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED {
				detail := buildPlaywrightDetail(res)
				r.Failure = &StructuredFailure{
					Message: extractFirstLine(detail),
					Detail:  detail,
					Kind:    classifyPlaywrightStatus(res.Status),
				}
			}
			c.Retries = append(c.Retries, r)
		}
	}
	return c
}

// mapPlaywrightOutcome maps the playwright per-spec aggregate status
// ("expected" | "unexpected" | "flaky" | "skipped") to the platform's
// TestCaseState. "expected" = the test outcome matched what playwright
// expected (typically passed). "unexpected" = the test failed when
// expected to pass (or vice versa for retry tests). "flaky" = passed
// after retries.
func mapPlaywrightOutcome(s string) runtimev0.TestCaseState {
	switch s {
	case "expected":
		return runtimev0.TestCaseState_TEST_CASE_STATE_PASSED
	case "unexpected":
		return runtimev0.TestCaseState_TEST_CASE_STATE_FAILED
	case "flaky":
		// Flaky = eventually passed. Surface as PASSED but with
		// retries[] populated so consumers can flag the test for
		// quarantine.
		return runtimev0.TestCaseState_TEST_CASE_STATE_PASSED
	case "skipped":
		return runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED
	default:
		return runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED
	}
}

// mapPlaywrightResultStatus maps a per-attempt status to TestCaseState.
func mapPlaywrightResultStatus(s string) runtimev0.TestCaseState {
	switch s {
	case "passed":
		return runtimev0.TestCaseState_TEST_CASE_STATE_PASSED
	case "failed":
		return runtimev0.TestCaseState_TEST_CASE_STATE_FAILED
	case "timedOut":
		return runtimev0.TestCaseState_TEST_CASE_STATE_FAILED
	case "skipped":
		return runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED
	default:
		return runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED
	}
}

// classifyPlaywrightStatus picks the FailureKind from the per-result
// status when the case failed.
func classifyPlaywrightStatus(s string) runtimev0.TestFailureKind {
	switch s {
	case "timedOut":
		return runtimev0.TestFailureKind_TEST_FAILURE_KIND_TIMEOUT
	default:
		return runtimev0.TestFailureKind_TEST_FAILURE_KIND_ASSERTION
	}
}

// buildPlaywrightDetail concatenates the error message + stack +
// captured stdout/stderr into the case's failure.detail.
func buildPlaywrightDetail(res playwrightResult) string {
	var b strings.Builder
	if res.Error != nil {
		b.WriteString(strings.TrimSpace(res.Error.Message))
		if res.Error.Stack != "" {
			b.WriteString("\n")
			b.WriteString(strings.TrimSpace(res.Error.Stack))
		}
	}
	if len(res.Stdout) > 0 {
		b.WriteString("\n--- stdout ---\n")
		for _, s := range res.Stdout {
			b.WriteString(s.Text)
		}
	}
	if len(res.Stderr) > 0 {
		b.WriteString("\n--- stderr ---\n")
		for _, s := range res.Stderr {
			b.WriteString(s.Text)
		}
	}
	return b.String()
}
