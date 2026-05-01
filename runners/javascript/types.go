package javascript

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// MaxCapturedOutputBytesPerCase mirrors the Go-runner + python-runner
// caps. Vitest/jest's `failureMessages[]` are typically a few KB
// (assertion message + Node stack trace); 32KiB allows fairly deep
// stacks without bloating the response.
const MaxCapturedOutputBytesPerCase = 32 * 1024

// StructuredTestRun is the JS-side equivalent of the per-runner
// structured test types in core/runners/golang and core/runners/python.
// Building one is the parser's job; converting to proto is shared.
type StructuredTestRun struct {
	// Suites — one per source file. Vitest/jest emit one
	// `testResults` entry per file; playwright nests differently
	// but we flatten to file-level for proto consistency.
	Suites []*StructuredSuite

	// CoveragePct is scraped from the runner's coverage summary
	// (vitest --coverage prints `All files | NN.NN%`; jest's
	// --coverage prints similar). 0 when not requested.
	CoveragePct float32

	truncatedCases int32
}

// StructuredSuite is one source-file's worth of cases.
type StructuredSuite struct {
	// Name is typically the file path; vitest/jest use absolute
	// paths in their JSON.
	Name     string
	File     string
	Duration time.Duration
	Cases    []*StructuredCase
}

// StructuredCase mirrors the python+go shape. Adds Retries for
// playwright's repeated-attempts-on-the-same-test feature.
type StructuredCase struct {
	Name      string
	FullName  string
	State     runtimev0.TestCaseState
	Duration  time.Duration
	File      string
	Line      int32
	Output    string
	Truncated bool
	Failure   *StructuredFailure
	// Retries is non-empty when the runner re-ran this case
	// (playwright `retries`). The case's top-level State reflects
	// the FINAL attempt; per-attempt detail lives here.
	Retries []*StructuredRetry
}

// StructuredFailure: same shape as the python parser's.
type StructuredFailure struct {
	Message string
	Detail  string
	Kind    runtimev0.TestFailureKind
}

// StructuredRetry is one attempt of a flaky test.
type StructuredRetry struct {
	Attempt  int32
	State    runtimev0.TestCaseState
	Duration time.Duration
	Failure  *StructuredFailure
}

// --- Conversion to protobuf TestResponse ---------------------------

// ToProtoResponse constructs the runtimev0.TestResponse with both
// structured (preferred) and legacy fields populated.
//
// runner is "vitest" | "jest" | "playwright"; suiteName echoes
// TestRequest.suite. duration is the wall-clock for the whole run.
func (r *StructuredTestRun) ToProtoResponse(runner, suiteName string, duration time.Duration) *runtimev0.TestResponse {
	suites := make([]*runtimev0.TestSuite, 0, len(r.Suites))
	counts := &runtimev0.TestCounts{}
	legacyFailures := make([]string, 0)

	// Stable suite ordering for deterministic output.
	sort.Slice(r.Suites, func(i, j int) bool { return r.Suites[i].File < r.Suites[j].File })

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
					fmt.Sprintf("FAIL %s > %s\n%s", s.File, pc.GetName(), pc.GetCapturedOutput()))
			}
		}
	}

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

		// Legacy flat (deprecated; populated for transition).
		Status:       &runtimev0.TestStatus{State: legacyState, Message: runResult.Message},
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
			RawArtifactFormat: jsCoverageFormat(runner),
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

	// Stable case ordering — alphabetical by full_name (vitest+jest
	// don't guarantee a particular order across runs).
	sort.Slice(s.Cases, func(i, j int) bool {
		return s.Cases[i].FullName < s.Cases[j].FullName
	})

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
		}
		if len(c.Retries) > 0 {
			for _, r := range c.Retries {
				pr := &runtimev0.TestRetry{
					Attempt:  r.Attempt,
					State:    r.State,
					Duration: durationpb.New(r.Duration),
				}
				if r.Failure != nil {
					pr.Failure = &runtimev0.TestFailure{
						Message: r.Failure.Message,
						Detail:  r.Failure.Detail,
						Kind:    r.Failure.Kind,
					}
				}
				pc.Retries = append(pc.Retries, pr)
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

// LegacyTestSummary returns the flat shape pre-overhaul callers used.
// Computed from the structured tree — single source of truth.
type LegacyTestSummary struct {
	Run      int32
	Passed   int32
	Failed   int32
	Skipped  int32
	Coverage float32
	Failures []string
}

// SummaryLine returns "N passed, M failed, K skipped" — used by
// agent log lines.
func (s *LegacyTestSummary) SummaryLine() string {
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

// LegacyTestSummary builds the flat summary from the structured tree.
func (r *StructuredTestRun) LegacyTestSummary() *LegacyTestSummary {
	s := &LegacyTestSummary{Coverage: r.CoveragePct}
	for _, suite := range r.Suites {
		for _, c := range suite.Cases {
			s.Run++
			switch c.State {
			case runtimev0.TestCaseState_TEST_CASE_STATE_PASSED:
				s.Passed++
			case runtimev0.TestCaseState_TEST_CASE_STATE_FAILED:
				s.Failed++
				s.Failures = append(s.Failures, fmt.Sprintf("FAIL %s > %s\n%s",
					suite.File, c.Name, c.Output))
			case runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED:
				s.Skipped++
			case runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED:
				s.Failed++ // legacy lumps errored under failed
				s.Failures = append(s.Failures, fmt.Sprintf("ERROR %s > %s\n%s",
					suite.File, c.Name, c.Output))
			}
		}
	}
	return s
}

// jsCoverageFormat names the raw artifact format the runner produces.
// vitest uses istanbul-format JSON / lcov; jest similarly; playwright
// has its own JSON. The format hint helps consumers know how to
// parse the raw artifact if they have it.
func jsCoverageFormat(runner string) string {
	switch runner {
	case "playwright":
		return "playwright-coverage"
	case "jest":
		return "istanbul"
	case "vitest":
		return "istanbul"
	default:
		return "lcov"
	}
}
