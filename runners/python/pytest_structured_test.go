package python_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/runners/python"
)

// realisticJUnitPassing mirrors the actual XML pytest emits with
// --junitxml when every test passes. Pulled from a real pytest 8.x
// run to keep this test honest against the format.
const realisticJUnitPassing = `<?xml version="1.0" encoding="utf-8"?>
<testsuites>
  <testsuite name="pytest" errors="0" failures="0" skipped="0" tests="2" time="0.045" timestamp="2026-04-30T20:00:00.000000" hostname="dev">
    <testcase classname="tests.test_users" name="test_create" time="0.012" file="tests/test_users.py" line="10" />
    <testcase classname="tests.test_users" name="test_delete" time="0.020" file="tests/test_users.py" line="25" />
  </testsuite>
</testsuites>`

const realisticJUnitMixed = `<?xml version="1.0" encoding="utf-8"?>
<testsuites>
  <testsuite name="pytest" errors="0" failures="1" skipped="1" tests="3" time="0.123">
    <testcase classname="tests.test_admin" name="test_pass" time="0.012" file="tests/test_admin.py" line="10" />
    <testcase classname="tests.test_admin" name="test_fail" time="0.020" file="tests/test_admin.py" line="25">
      <failure message="assert 1 == 2" type="AssertionError">tests/test_admin.py:25: in test_fail
    assert 1 == 2
E   assert 1 == 2
</failure>
    </testcase>
    <testcase classname="tests.test_admin" name="test_skip" time="0.000" file="tests/test_admin.py" line="40">
      <skipped message="needs internet" type="pytest.skip" />
    </testcase>
  </testsuite>
</testsuites>`

const realisticJUnitErrored = `<?xml version="1.0" encoding="utf-8"?>
<testsuites>
  <testsuite name="pytest" errors="1" failures="0" skipped="0" tests="1" time="0.005">
    <testcase classname="tests.test_setup" name="test_with_broken_fixture" time="0" file="tests/test_setup.py" line="15">
      <error message="fixture 'db' not found" type="FixtureLookupError">tests/test_setup.py:15: fixture 'db' not found</error>
    </testcase>
  </testsuite>
</testsuites>`

// --- Pure-XML parser tests (no live pytest required) ---------------

func TestPytestStructured_AllPassing_NoCapturedOutput(t *testing.T) {
	run := python.ParsePytestJUnit(realisticJUnitPassing, 0)
	resp := run.ToProtoResponse("pytest", "unit", time.Second)

	require.Equal(t, runtimev0.TestRunResult_PASSED, resp.Result.State)
	require.EqualValues(t, 2, resp.Counts.Total)
	require.EqualValues(t, 2, resp.Counts.Passed)
	require.EqualValues(t, 0, resp.Counts.Failed)

	require.Len(t, resp.Suites, 1, "two cases in one file → one suite")
	suite := resp.Suites[0]
	require.Equal(t, "tests/test_users.py", suite.File)
	require.Len(t, suite.Cases, 2)

	for _, c := range suite.Cases {
		require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_PASSED, c.State)
		require.Empty(t, c.CapturedOutput,
			"PASSED cases must not carry captured output (the load-bearing rule)")
		require.Nil(t, c.Failure)
	}

	// Location set on each case.
	require.Equal(t, "tests/test_users.py", suite.Cases[0].Location.File)
	require.EqualValues(t, 10, suite.Cases[0].Location.Line)
}

func TestPytestStructured_FailedCase_CarriesDetailAndKind(t *testing.T) {
	run := python.ParsePytestJUnit(realisticJUnitMixed, 0)
	resp := run.ToProtoResponse("pytest", "", time.Second)

	require.Equal(t, runtimev0.TestRunResult_FAILED, resp.Result.State)
	require.EqualValues(t, 3, resp.Counts.Total)
	require.EqualValues(t, 1, resp.Counts.Passed)
	require.EqualValues(t, 1, resp.Counts.Failed)
	require.EqualValues(t, 1, resp.Counts.Skipped)

	suite := resp.Suites[0]
	// Cases sorted alphabetically: test_fail, test_pass, test_skip
	cases := caseByName(t, suite)

	pc := cases["test_pass"]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_PASSED, pc.State)
	require.Empty(t, pc.CapturedOutput)

	fc := cases["test_fail"]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_FAILED, fc.State)
	require.NotNil(t, fc.Failure)
	require.Equal(t, "assert 1 == 2", fc.Failure.Message,
		"failure.message comes from the JUnit `message` attribute (one-liner)")
	require.Contains(t, fc.Failure.Detail, "tests/test_admin.py:25",
		"failure.detail must contain the traceback for IDE jump")
	require.Equal(t, runtimev0.TestFailureKind_TEST_FAILURE_KIND_ASSERTION, fc.Failure.Kind)
	require.NotEmpty(t, fc.CapturedOutput,
		"FAILED cases get the failure body in captured_output")

	sc := cases["test_skip"]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED, sc.State)
	require.Empty(t, sc.CapturedOutput,
		"SKIPPED cases follow the same retention rule as PASSED")
	require.NotNil(t, sc.Failure, "skip reason lives on the same struct so consumers have one place to look")
	require.Equal(t, "needs internet", sc.Failure.SkipReason)
}

func TestPytestStructured_FixtureError_ClassifiedAsSetup(t *testing.T) {
	run := python.ParsePytestJUnit(realisticJUnitErrored, 0)
	resp := run.ToProtoResponse("pytest", "", time.Second)

	require.Equal(t, runtimev0.TestRunResult_ERRORED, resp.Result.State,
		"any errored case bubbles up to ERRORED at the run level (distinct from FAILED)")
	require.EqualValues(t, 1, resp.Counts.Errored)
	require.EqualValues(t, 0, resp.Counts.Failed)

	c := resp.Suites[0].Cases[0]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED, c.State)
	require.Equal(t, runtimev0.TestFailureKind_TEST_FAILURE_KIND_SETUP, c.Failure.Kind,
		"`<error>` in pytest junit means fixture/setup failure, not assertion")
	require.Contains(t, c.Failure.Detail, "fixture 'db' not found")
	require.NotEmpty(t, c.CapturedOutput)
}

func TestPytestStructured_TimeoutClassified(t *testing.T) {
	const xml = `<?xml version="1.0"?>
<testsuites><testsuite name="pytest" tests="1" failures="1">
  <testcase classname="tests.t" name="test_slow" time="30" file="tests/t.py" line="5">
    <failure message="Timeout >30.0s" type="Failed">tests/t.py:5: Timeout &gt;30.0s
during call</failure>
  </testcase>
</testsuite></testsuites>`
	run := python.ParsePytestJUnit(xml, 0)
	resp := run.ToProtoResponse("pytest", "", time.Second)
	c := resp.Suites[0].Cases[0]
	require.Equal(t, runtimev0.TestFailureKind_TEST_FAILURE_KIND_TIMEOUT, c.Failure.Kind,
		"timeout markers in body must classify as TIMEOUT, not ASSERTION")
}

func TestPytestStructured_OutputCappedAtMaxBytes(t *testing.T) {
	// Build a failure with body larger than the cap. Embed a 40KB
	// payload inside the `<failure>` body.
	huge := strings.Repeat("x", python.MaxCapturedOutputBytesPerCase+8*1024)
	xml := `<?xml version="1.0"?>
<testsuites><testsuite name="pytest" tests="1" failures="1">
  <testcase classname="tests.t" name="test_fat" time="0.1" file="tests/t.py" line="5">
    <failure message="big" type="AssertionError">` + huge + `</failure>
  </testcase>
</testsuite></testsuites>`
	run := python.ParsePytestJUnit(xml, 0)
	resp := run.ToProtoResponse("pytest", "", time.Second)

	c := resp.Suites[0].Cases[0]
	require.LessOrEqual(t, len(c.CapturedOutput), python.MaxCapturedOutputBytesPerCase+128,
		"captured_output must respect the cap + small marker overhead")
	require.True(t, resp.Truncation.Happened,
		"truncation block must surface — silent truncation is the bug we're avoiding")
	require.EqualValues(t, 1, resp.Truncation.TruncatedCases)
}

func TestPytestStructured_LegacyFieldsMirrorStructured(t *testing.T) {
	run := python.ParsePytestJUnit(realisticJUnitMixed, 87.5)
	resp := run.ToProtoResponse("pytest", "unit", time.Second)

	// Legacy compat — old consumers that read flat fields still work.
	require.EqualValues(t, 3, resp.TestsRun)
	require.EqualValues(t, 1, resp.TestsPassed)
	require.EqualValues(t, 1, resp.TestsFailed)
	require.EqualValues(t, 1, resp.TestsSkipped)
	require.InDelta(t, 87.5, resp.CoveragePct, 0.001)
	require.Equal(t, runtimev0.TestStatus_ERROR, resp.Status.State,
		"any failure flips legacy status to ERROR")
	require.Len(t, resp.Failures, 1)
	require.Contains(t, resp.Failures[0], "tests/test_admin.py")
	require.Contains(t, resp.Failures[0], "test_fail")

	// Structured equivalent
	require.NotNil(t, resp.Coverage)
	require.InDelta(t, 87.5, resp.Coverage.TotalPct, 0.001)
	require.Equal(t, "pytest-cov", resp.Coverage.RawArtifactFormat)
}

func TestPytestStructured_CasesGroupedByFile(t *testing.T) {
	const xml = `<?xml version="1.0"?>
<testsuites><testsuite name="pytest" tests="3" failures="0">
  <testcase classname="b" name="testB" time="0" file="b.py" line="1" />
  <testcase classname="a" name="testA1" time="0" file="a.py" line="1" />
  <testcase classname="a" name="testA2" time="0" file="a.py" line="2" />
</testsuite></testsuites>`
	run := python.ParsePytestJUnit(xml, 0)
	resp := run.ToProtoResponse("pytest", "", time.Second)

	require.Len(t, resp.Suites, 2)
	// Alphabetical suite ordering — a.py before b.py.
	require.Equal(t, "a.py", resp.Suites[0].File)
	require.Equal(t, "b.py", resp.Suites[1].File)

	require.Len(t, resp.Suites[0].Cases, 2,
		"both a-file cases land in the a.py suite (file-grouped, not class-grouped)")
	require.Len(t, resp.Suites[1].Cases, 1)
}

func TestPytestStructured_EmptyXML_ReturnsEmptyRun(t *testing.T) {
	// Pytest collection failure produces an empty / missing JUnit
	// file. Parser must not panic — return an empty StructuredTestRun.
	run := python.ParsePytestJUnit("", 0)
	resp := run.ToProtoResponse("pytest", "", 0)

	require.Empty(t, resp.Suites)
	require.EqualValues(t, 0, resp.Counts.Total)
	require.Equal(t, runtimev0.TestRunResult_PASSED, resp.Result.State,
		"empty run is technically passed (zero failures); upstream uses runErr to know pytest crashed")
}

func TestPytestStructured_LegacySummary_RoundTripsCorrectCounts(t *testing.T) {
	run := python.ParsePytestJUnit(realisticJUnitMixed, 87.5)
	s := run.LegacyTestSummary()

	require.EqualValues(t, 3, s.Run)
	require.EqualValues(t, 1, s.Passed)
	require.EqualValues(t, 1, s.Failed)
	require.EqualValues(t, 1, s.Skipped)
	require.InDelta(t, 87.5, s.Coverage, 0.001)
	require.Len(t, s.Failures, 1)
	// The flat Failures list carries enough detail for the existing
	// log-the-failure consumer surface.
	require.Contains(t, s.Failures[0], "FAIL")
	require.Contains(t, s.Failures[0], "test_fail")
}

// caseByName returns a map of case name → *TestCase for the suite's
// cases. Helps tests assert on specific cases without depending on
// alphabetical ordering inside the assertions.
func caseByName(t *testing.T, suite *runtimev0.TestSuite) map[string]*runtimev0.TestCase {
	t.Helper()
	out := make(map[string]*runtimev0.TestCase, len(suite.Cases))
	for _, c := range suite.Cases {
		out[c.Name] = c
	}
	return out
}
