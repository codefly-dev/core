package python

import (
	"testing"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// Real django/unittest TextTestRunner verbose output (verbosity=2): single-line
// results, a docstring two-line form, and FAIL:/ERROR: blocks with tracebacks.
const djangoVerboseOutput = `test_charfield (model_fields.test_charfield.TestCharField) ... ok
test_max_length (model_fields.test_charfield.TestCharField) ... FAIL
test_setup (model_fields.test_charfield.TestCharField) ... ERROR
test_skips (model_fields.test_charfield.TestCharField) ... skipped 'needs db'
test_doc (auth_tests.test_views.LoginTest)
First line of the docstring. ... ok

======================================================================
FAIL: test_max_length (model_fields.test_charfield.TestCharField)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "tests/model_fields/test_charfield.py", line 42, in test_max_length
    self.assertEqual(field.max_length, 10)
AssertionError: 20 != 10
======================================================================
ERROR: test_setup (model_fields.test_charfield.TestCharField)
----------------------------------------------------------------------
Traceback (most recent call last):
RuntimeError: boom
----------------------------------------------------------------------
Ran 5 tests in 0.012s

FAILED (failures=1, errors=1, skipped=1)
`

func caseByFullName(run *StructuredTestRun, full string) *StructuredCase {
	for _, s := range run.Suites {
		for _, c := range s.Cases {
			if c.FullName == full {
				return c
			}
		}
	}
	return nil
}

func TestParseUnittestText_States(t *testing.T) {
	run := ParseUnittestText(djangoVerboseOutput)

	want := map[string]runtimev0.TestCaseState{
		"model_fields.test_charfield.TestCharField.test_charfield":  runtimev0.TestCaseState_TEST_CASE_STATE_PASSED,
		"model_fields.test_charfield.TestCharField.test_max_length": runtimev0.TestCaseState_TEST_CASE_STATE_FAILED,
		"model_fields.test_charfield.TestCharField.test_setup":      runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED,
		"model_fields.test_charfield.TestCharField.test_skips":      runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED,
		"auth_tests.test_views.LoginTest.test_doc":                  runtimev0.TestCaseState_TEST_CASE_STATE_PASSED,
	}
	for full, st := range want {
		c := caseByFullName(run, full)
		if c == nil {
			t.Errorf("missing case %s", full)
			continue
		}
		if c.State != st {
			t.Errorf("%s state = %v, want %v", full, c.State, st)
		}
	}

	// Counts via the proto response (what Mind actually reads).
	resp := run.ToProtoResponse("django", "", 0)
	if resp.Counts.Total != 5 || resp.Counts.Passed != 2 || resp.Counts.Failed != 1 ||
		resp.Counts.Errored != 1 || resp.Counts.Skipped != 1 {
		t.Fatalf("counts = %+v, want total5 pass2 fail1 err1 skip1", resp.Counts)
	}
	if resp.Result.State != runtimev0.TestRunResult_ERRORED { // errored dominates
		t.Errorf("run result = %v, want ERRORED", resp.Result.State)
	}
}

// The FAIL block's traceback must attach to the failing case as captured output.
func TestParseUnittestText_FailureDetail(t *testing.T) {
	run := ParseUnittestText(djangoVerboseOutput)
	c := caseByFullName(run, "model_fields.test_charfield.TestCharField.test_max_length")
	if c == nil || c.Failure == nil {
		t.Fatal("expected failing case with failure detail")
	}
	if want := "AssertionError: 20 != 10"; !contains(c.Failure.Detail, want) {
		t.Errorf("failure detail missing %q: %q", want, c.Failure.Detail)
	}
}

// Empty / non-test output → zero suites (the env-block signal for the caller).
func TestParseUnittestText_Empty(t *testing.T) {
	if run := ParseUnittestText(""); len(run.Suites) != 0 {
		t.Fatalf("empty output should yield no suites, got %d", len(run.Suites))
	}
	if run := ParseUnittestText("ImportError: No module named django\n"); len(run.Suites) != 0 {
		t.Fatalf("collection error should yield no suites, got %d", len(run.Suites))
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
