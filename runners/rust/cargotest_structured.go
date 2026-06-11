package rust

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// MaxCapturedOutputBytesPerCase caps per-case captured output stored in the
// structured TestResponse. Mirrors golang.MaxCapturedOutputBytesPerCase.
const MaxCapturedOutputBytesPerCase = 32 * 1024 // 32 KiB

// StructuredTestRun is the hierarchical representation built by walking
// `cargo test` text output. One suite per test binary ("Running …" marker).
// Convertible to runtimev0.TestResponse via ToProtoResponse. Mirrors
// golang.StructuredTestRun.
type StructuredTestRun struct {
	Started        time.Time
	Suites         map[string]*structuredSuite
	CoveragePct    float32
	truncatedCases int32

	order []string // suite insertion order (stable fallback to alpha at emit)
}

type structuredSuite struct {
	Name        string
	cases       map[string]*structuredCase
	caseOrder   []string
	elapsed     time.Duration
	errored     bool
	errorReason string
}

type structuredCase struct {
	Name      string
	state     runtimev0.TestCaseState
	output    strings.Builder
	truncated bool
}

// `test result: FAILED. 1 passed; 1 failed; 1 ignored; 0 measured; 0 filtered out; finished in 0.01s`
var resultLineRe = regexp.MustCompile(`^test result:.*finished in ([\d.]+)s`)

// ParseCargoTestStructured walks `cargo test` text output and returns the
// structured representation. Mirrors golang.ParseTestJSONStructured.
func ParseCargoTestStructured(raw string) *StructuredTestRun {
	r := &StructuredTestRun{
		Suites:  make(map[string]*structuredSuite),
		Started: time.Now(),
	}
	suiteName := ""
	captureKey := ""

	for _, line := range strings.Split(raw, "\n") {
		if m := runningRe.FindStringSubmatch(line); m != nil {
			suiteName = strings.TrimSpace(m[1])
			r.suite(suiteName)
			captureKey = ""
			continue
		}
		if m := docTestsRe.FindStringSubmatch(line); m != nil {
			suiteName = "doc-tests " + strings.TrimSpace(m[1])
			r.suite(suiteName)
			captureKey = ""
			continue
		}

		if m := resultLineRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			if s, ok := r.Suites[suiteName]; ok {
				var secs float64
				_, _ = fmt.Sscanf(m[1], "%f", &secs)
				s.elapsed = time.Duration(secs * float64(time.Second))
			}
			continue
		}

		// Failure detail capture.
		if m := failHeaderRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			captureKey = m[1]
			continue
		}
		if captureKey != "" {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "----") || strings.HasPrefix(trimmed, "failures:") || strings.HasPrefix(trimmed, "test result:") {
				captureKey = ""
			} else if s, ok := r.Suites[suiteName]; ok {
				c := s.caseFor(captureKey)
				appendCapped(&c.output, &c.truncated, line+"\n")
			}
		}

		m := testLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if suiteName == "" {
			suiteName = "<tests>"
			r.suite(suiteName)
		}
		s := r.suite(suiteName)
		c := s.caseFor(m[1])
		switch m[2] {
		case "ok":
			c.state = runtimev0.TestCaseState_TEST_CASE_STATE_PASSED
		case "FAILED":
			c.state = runtimev0.TestCaseState_TEST_CASE_STATE_FAILED
		case "ignored":
			c.state = runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED
		}
	}

	for _, suite := range r.Suites {
		for _, c := range suite.cases {
			if c.truncated {
				r.truncatedCases++
			}
		}
	}
	return r
}

func (r *StructuredTestRun) suite(name string) *structuredSuite {
	s, ok := r.Suites[name]
	if !ok {
		s = &structuredSuite{Name: name, cases: make(map[string]*structuredCase)}
		r.Suites[name] = s
		r.order = append(r.order, name)
	}
	return s
}

func (s *structuredSuite) caseFor(name string) *structuredCase {
	c, ok := s.cases[name]
	if !ok {
		c = &structuredCase{Name: name, state: runtimev0.TestCaseState_TEST_CASE_STATE_UNSPECIFIED}
		s.cases[name] = c
		s.caseOrder = append(s.caseOrder, name)
	}
	return c
}

// appendCapped appends s to dst up to MaxCapturedOutputBytesPerCase.
// Mirrors golang.appendCapped.
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

// ToProtoResponse constructs runtimev0.TestResponse with the structured tree
// plus the legacy flat fields. Mirrors golang.StructuredTestRun.ToProtoResponse.
// runner is the runner identifier ("cargo-test"); suiteName echoes
// TestRequest.suite; duration is the wall-clock for the whole run.
func (r *StructuredTestRun) ToProtoResponse(runner, suiteName string, duration time.Duration) *runtimev0.TestResponse {
	suites := make([]*runtimev0.TestSuite, 0, len(r.Suites))
	counts := &runtimev0.TestCounts{}
	legacyFailures := make([]string, 0)
	var legacyOutput strings.Builder

	names := make([]string, 0, len(r.Suites))
	names = append(names, r.order...)
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

		for _, pc := range ps.GetCases() {
			if pc.GetState() == runtimev0.TestCaseState_TEST_CASE_STATE_FAILED ||
				pc.GetState() == runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED {
				legacyFailures = append(legacyFailures,
					fmt.Sprintf("FAIL %s/%s\n%s", name, pc.GetName(), pc.GetCapturedOutput()))
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

		Status:       &runtimev0.TestStatus{State: legacyState, Message: runResult.Message},
		Output:       legacyOutput.String(),
		TestsRun:     counts.Total,
		TestsPassed:  counts.Passed,
		TestsFailed:  counts.Failed,
		TestsSkipped: counts.Skipped,
		CoveragePct:  r.CoveragePct,
		Failures:     legacyFailures,
	}
	return resp
}

func (s *structuredSuite) toProto() (*runtimev0.TestSuite, *runtimev0.TestCounts) {
	ps := &runtimev0.TestSuite{
		Name:     s.Name,
		Duration: durationpb.New(s.elapsed),
	}
	suiteCounts := &runtimev0.TestCounts{}

	names := make([]string, 0, len(s.cases))
	names = append(names, s.caseOrder...)
	sortStrings(names)

	for _, name := range names {
		c := s.cases[name]
		pc := &runtimev0.TestCase{
			Name:     c.Name,
			FullName: s.Name + "." + c.Name,
			State:    c.state,
		}
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

	ps.Counts = suiteCounts
	return ps, suiteCounts
}

// extractFailureMessage picks the assertion message out of per-case output.
// Heuristic: the panic line, else the first non-blank line. Mirrors the Go
// helper but tuned for Rust's `thread '…' panicked at …` convention.
func extractFailureMessage(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.Contains(trim, "panicked at") || strings.HasPrefix(trim, "assertion") {
			return trim
		}
	}
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim != "" {
			return trim
		}
	}
	return ""
}

// extractFailureKind classifies the failure from output markers.
func extractFailureKind(output string) runtimev0.TestFailureKind {
	if strings.Contains(output, "panicked at") || strings.Contains(output, "panic") {
		return runtimev0.TestFailureKind_TEST_FAILURE_KIND_PANIC
	}
	if strings.Contains(output, "timed out") || strings.Contains(output, "deadline") {
		return runtimev0.TestFailureKind_TEST_FAILURE_KIND_TIMEOUT
	}
	return runtimev0.TestFailureKind_TEST_FAILURE_KIND_ASSERTION
}

// sortStrings is an inlined insertion sort — same shim as the Go package.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
