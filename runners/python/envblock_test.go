package python

import (
	"errors"
	"strings"
	"testing"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// The python plugin owns the distinction between "tests RAN and failed" and "the
// ENVIRONMENT could not run them". These tests lock that classification so a
// caller (the Mind tooling inner loop) reads pass/fail/blocked from the STRUCTURE
// (Result.State) — never from a raw "exit status 1".

func TestClassifyEnvError(t *testing.T) {
	cases := []struct {
		name, raw, wantReason string
	}{
		{"module-not-found", "ModuleNotFoundError: No module named 'werkzeug'", "missing-dependency"},
		{"import-error", "ImportError: cannot import name 'url_quote' from 'werkzeug.urls'", "import-error"},
		{"version-conflict", "error: No matching distribution found for Werkzeug<3 (incompatible)", "version-conflict"},
		{"interpreter-missing", "error: No interpreter found for Python 3.6", "interpreter-missing"},
		{"unknown", "Segmentation fault (core dumped)", "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyEnvError(c.raw, errors.New("exit status 1"))
			if got.Reason != c.wantReason {
				t.Fatalf("reason = %q, want %q", got.Reason, c.wantReason)
			}
			if got.Detail == "" {
				t.Fatalf("detail should carry the scraped failure line")
			}
		})
	}
}

// A run that produced ZERO cases but carries an EnvError is ERRORED — NOT the
// "all passed" default that zero counts would otherwise yield. This is the exact
// misclassification fix: a blocked environment must not read as a green run.
func TestToProtoResponse_EnvErrorIsErroredNotPassed(t *testing.T) {
	run := &StructuredTestRun{
		EnvError: &RunEnvError{Reason: "missing-dependency", Detail: "No module named 'werkzeug'"},
	}
	resp := run.ToProtoResponse("python", "flask", 0)
	if resp.GetResult().GetState() != runtimev0.TestRunResult_ERRORED {
		t.Fatalf("env-blocked run must be ERRORED, got %v", resp.GetResult().GetState())
	}
	if resp.GetCounts().GetTotal() != 0 {
		t.Fatalf("env-blocked run executed no cases, got total=%d", resp.GetCounts().GetTotal())
	}
	if !strings.Contains(resp.GetResult().GetMessage(), "missing-dependency") {
		t.Fatalf("message should carry the classified reason, got %q", resp.GetResult().GetMessage())
	}
}

// A run with cases that failed stays FAILED (tests ran) — the env-error path must
// not hijack a real test failure.
func TestToProtoResponse_RealFailureStaysFailed(t *testing.T) {
	run := &StructuredTestRun{
		Suites: []*StructuredSuite{{
			Name: "test_x.py", File: "test_x.py",
			Cases: []*StructuredCase{
				{Name: "test_a", State: runtimev0.TestCaseState_TEST_CASE_STATE_PASSED},
				{Name: "test_b", State: runtimev0.TestCaseState_TEST_CASE_STATE_FAILED},
			},
		}},
	}
	resp := run.ToProtoResponse("python", "x", 0)
	if resp.GetResult().GetState() != runtimev0.TestRunResult_FAILED {
		t.Fatalf("a real test failure must be FAILED, got %v", resp.GetResult().GetState())
	}
	if resp.GetCounts().GetTotal() != 2 || resp.GetCounts().GetFailed() != 1 {
		t.Fatalf("counts wrong: %+v", resp.GetCounts())
	}
}
