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
		// A SyntaxError in a (mal-edited) test file is a CODE defect, NOT an env
		// block — even though pytest reports it as a "collection error". It must
		// classify distinctly so the tooling loop does not try to heal provisioning.
		{"syntax-error is code not env", "ERROR collecting tests/test_blueprints.py\nE   SyntaxError: invalid syntax\n!!! collection error !!!", "test-collection-error"},
		{"indentation-error is code not env", "IndentationError: unexpected indent\n!!! collection error !!!", "test-collection-error"},
		// A collection error caused by a missing IMPORT is still an env block.
		{"import-driven collection error stays env", "ImportError while importing test module\n!!! collection error !!!", "import-error"},
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

// The ACTUAL flask-5014 case: flask 2.x needs Werkzeug<3, uv can't resolve, and
// its terse last line ("your requirements are unsatisfiable") names nothing. The
// classifier must (a) call it a version-conflict and (b) keep enough DETAIL that
// the conflicting package is named — otherwise no remediator (config OR file
// edit) can know to pin Werkzeug<3.
func TestClassifyEnvError_FlaskWerkzeugResolutionNamesPackage(t *testing.T) {
	raw := strings.Join([]string{
		"  × No solution found when resolving dependencies:",
		"  ╰─▶ Because flask==2.0.1 depends on werkzeug>=2.0,<2.1 and your project",
		"      depends on werkzeug>=3.0.0, we can conclude that your project's",
		"      requirements are unsatisfiable.",
	}, "\n")
	got := ClassifyEnvError(raw, errors.New("exit status 1"))
	if got == nil || got.Reason != "version-conflict" {
		t.Fatalf("flask werkzeug resolution conflict must be version-conflict, got %+v", got)
	}
	if !strings.Contains(strings.ToLower(got.Detail), "werkzeug") {
		t.Fatalf("detail must NAME the conflicting package so a remediator can pin it; got %q", got.Detail)
	}
}

func TestClassifyEnvError_IgnoresPipNoticeFooter(t *testing.T) {
	raw := strings.Join([]string{
		"ERROR: Could not find a version that satisfies the requirement selenium<4.0",
		"ERROR: No matching distribution found for selenium<4.0",
		"[notice] A new release of pip is available: 23.0 -> 24.0",
		"[notice] To update, run: pip install --upgrade pip",
	}, "\n")
	got := ClassifyEnvError(raw, errors.New("exit status 1"))
	if got == nil || got.Reason != "version-conflict" {
		t.Fatalf("pip-footer install failure must be version-conflict, got %+v", got)
	}
	if !strings.Contains(strings.ToLower(got.Detail), "selenium") {
		t.Fatalf("detail must keep the actionable install error, got %q", got.Detail)
	}
	if strings.Contains(strings.ToLower(got.Detail), "pip install --upgrade pip") {
		t.Fatalf("detail should not select the pip notice footer: %q", got.Detail)
	}
}

func TestClassifyEnvError_IgnoresProgressOnlyFooter(t *testing.T) {
	raw := strings.Join([]string{
		"FAILED tests/admin_inlines/tests.py::InlinePermissionTests::test_m2m_view_only",
		"...........................................F............................ [100%]",
		"[notice] To update, run: pip install --upgrade pip",
	}, "\n")
	got := ClassifyEnvError(raw, errors.New("exit status 1"))
	if got == nil || got.Reason != "unknown" {
		t.Fatalf("generic failed run should stay unknown env block, got %+v", got)
	}
	if !strings.Contains(got.Detail, "FAILED tests/admin_inlines/tests.py") {
		t.Fatalf("detail should select the failed-test diagnostic, got %q", got.Detail)
	}
	if strings.Contains(got.Detail, "........") {
		t.Fatalf("detail should not select progress-only output, got %q", got.Detail)
	}
}

func TestClassifyEnvError_PipNoticeOnlyFallsBackToRunError(t *testing.T) {
	got := ClassifyEnvError("[notice] To update, run: pip install --upgrade pip", errors.New("exit status 1"))
	if got == nil || got.Reason != "unknown" {
		t.Fatalf("notice-only output should stay unknown, got %+v", got)
	}
	if got.Detail != "exit status 1" {
		t.Fatalf("notice-only output should not become the detail, got %q", got.Detail)
	}
}

// A run that produced ZERO cases but carries an EnvError is ERRORED — NOT the
// "all passed" default that zero counts would otherwise yield. This is the exact
// misclassification fix: a blocked environment must not read as a green run.
// TestDetectSharedEnvFailure_WerkzeugVersionMismatch: the flask-5014 reality —
// werkzeug 3.x removed __version__, so every test that builds a Flask test client
// raises the IDENTICAL AttributeError at fixture setup while unrelated tests
// pass. That partial run hides the env block from the zero-collected detector;
// detectSharedEnvFailure catches the repeated dependency error so it heals.
func TestDetectSharedEnvFailure_WerkzeugVersionMismatch(t *testing.T) {
	raw := strings.Repeat(
		"tests/conftest.py:70: in client\n    return app.test_client()\n"+
			"src/flask/testing.py:117: in __init__\n"+
			"E   AttributeError: module 'werkzeug' has no attribute '__version__'\n\n", 5)
	ev := detectSharedEnvFailure(raw)
	if ev == nil {
		t.Fatal("expected a shared env failure, got nil")
	}
	if ev.Reason != "version-conflict" {
		t.Errorf("reason = %q, want version-conflict (installed-but-incompatible)", ev.Reason)
	}
	if !strings.Contains(ev.Detail, "werkzeug") {
		t.Errorf("detail should name the package: %q", ev.Detail)
	}
}

// A handful of genuine assertion failures must NOT be misread as an env block.
func TestDetectSharedEnvFailure_RealFailuresAreNotEnvBlock(t *testing.T) {
	raw := "E   assert 1 == 2\nE   AssertionError\nE   assert foo() is None\n"
	if ev := detectSharedEnvFailure(raw); ev != nil {
		t.Fatalf("assertion failures must not be an env block, got %+v", ev)
	}
	// A single import error (one test) is below the shared-error threshold.
	if ev := detectSharedEnvFailure("E   ModuleNotFoundError: No module named 'x'\n"); ev != nil {
		t.Fatalf("a single import error is not a SHARED env block, got %+v", ev)
	}
}

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

func TestToProtoResponse_EnvErrorPreservesRawOutput(t *testing.T) {
	run := &StructuredTestRun{
		RawOutput: "ERROR: Could not find a version that satisfies the requirement selenium<4.0\n[notice] To update, run: pip install --upgrade pip",
		EnvError:  &RunEnvError{Reason: "version-conflict", Detail: "No matching distribution found for selenium<4.0"},
	}
	resp := run.ToProtoResponse("python", "django", 0)
	if !strings.Contains(resp.GetOutput(), "selenium<4.0") {
		t.Fatalf("raw output should be preserved for env-block evidence, got %q", resp.GetOutput())
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
