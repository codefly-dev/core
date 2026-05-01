package javascript_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/runners/javascript"
)

// realisticVitestPassing is a literal vitest --reporter=json output
// for two passing tests in one file. The JSON shape is identical
// to jest's --json output (vitest copied jest's reporter format).
const realisticVitestPassing = `{
  "numTotalTestSuites": 1,
  "numPassedTestSuites": 1,
  "numFailedTestSuites": 0,
  "numTotalTests": 2,
  "numPassedTests": 2,
  "numFailedTests": 0,
  "numPendingTests": 0,
  "testResults": [
    {
      "name": "/abs/path/src/users.test.ts",
      "status": "passed",
      "startTime": 1714509600000,
      "endTime": 1714509600100,
      "assertionResults": [
        {
          "ancestorTitles": ["users"],
          "title": "creates a user",
          "fullName": "users > creates a user",
          "status": "passed",
          "duration": 12,
          "location": {"line": 10, "column": 3},
          "failureMessages": []
        },
        {
          "ancestorTitles": ["users"],
          "title": "deletes a user",
          "fullName": "users > deletes a user",
          "status": "passed",
          "duration": 18,
          "location": {"line": 25, "column": 3},
          "failureMessages": []
        }
      ]
    }
  ]
}`

const realisticVitestMixed = `{
  "numTotalTests": 3,
  "numPassedTests": 1,
  "numFailedTests": 1,
  "numPendingTests": 1,
  "testResults": [
    {
      "name": "/abs/path/src/auth.test.ts",
      "status": "failed",
      "startTime": 1714509600000,
      "endTime": 1714509600200,
      "assertionResults": [
        {
          "ancestorTitles": ["auth"],
          "title": "validates token",
          "fullName": "auth > validates token",
          "status": "passed",
          "duration": 8,
          "location": {"line": 5, "column": 3},
          "failureMessages": []
        },
        {
          "ancestorTitles": ["auth"],
          "title": "rejects expired",
          "fullName": "auth > rejects expired",
          "status": "failed",
          "duration": 15,
          "location": {"line": 20, "column": 3},
          "failureMessages": ["AssertionError: expected 'expired' to be 'valid'\n  at Object.<anonymous> (/abs/path/src/auth.test.ts:22:16)"]
        },
        {
          "ancestorTitles": ["auth"],
          "title": "skipped path",
          "fullName": "auth > skipped path",
          "status": "skipped",
          "duration": 0,
          "failureMessages": []
        }
      ]
    }
  ]
}`

const realisticVitestImportError = `{
  "numTotalTests": 0,
  "numFailedTests": 0,
  "testResults": [
    {
      "name": "/abs/path/src/broken.test.ts",
      "status": "failed",
      "startTime": 1714509600000,
      "endTime": 1714509600050,
      "assertionResults": [],
      "failureMessage": "SyntaxError: Cannot find module './missing-helper'\n    at /abs/path/src/broken.test.ts:1:1"
    }
  ]
}`

// --- Tests ---------------------------------------------------------

func TestVitest_AllPassing_NoCapturedOutput(t *testing.T) {
	run := javascript.ParseJestVitestJSON(realisticVitestPassing, 0)
	resp := run.ToProtoResponse("vitest", "unit", time.Second)

	require.Equal(t, runtimev0.TestRunResult_PASSED, resp.Result.State)
	require.EqualValues(t, 2, resp.Counts.Total)
	require.EqualValues(t, 2, resp.Counts.Passed)
	require.EqualValues(t, 0, resp.Counts.Failed)

	require.Len(t, resp.Suites, 1)
	suite := resp.Suites[0]
	require.Equal(t, "/abs/path/src/users.test.ts", suite.File)
	require.Len(t, suite.Cases, 2)

	for _, c := range suite.Cases {
		require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_PASSED, c.State)
		require.Empty(t, c.CapturedOutput,
			"PASSED cases get NO captured output (the load-bearing rule)")
		require.Nil(t, c.Failure)
	}

	// Locations populated from JSON `location.line`.
	require.NotNil(t, suite.Cases[0].Location)
	require.Greater(t, suite.Cases[0].Location.Line, int32(0))
}

func TestVitest_FailedCase_CarriesFailureMessage(t *testing.T) {
	run := javascript.ParseJestVitestJSON(realisticVitestMixed, 0)
	resp := run.ToProtoResponse("vitest", "", time.Second)

	require.Equal(t, runtimev0.TestRunResult_FAILED, resp.Result.State)
	require.EqualValues(t, 3, resp.Counts.Total)
	require.EqualValues(t, 1, resp.Counts.Passed)
	require.EqualValues(t, 1, resp.Counts.Failed)
	require.EqualValues(t, 1, resp.Counts.Skipped)

	suite := resp.Suites[0]
	cases := caseByName(t, suite)

	pc := cases["validates token"]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_PASSED, pc.State)
	require.Empty(t, pc.CapturedOutput)

	fc := cases["rejects expired"]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_FAILED, fc.State)
	require.NotNil(t, fc.Failure)
	require.Contains(t, fc.Failure.Detail, "AssertionError",
		"failure.detail must contain the failureMessages content for diagnosis")
	require.Equal(t, runtimev0.TestFailureKind_TEST_FAILURE_KIND_ASSERTION, fc.Failure.Kind)
	require.NotEmpty(t, fc.CapturedOutput)

	sc := cases["skipped path"]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED, sc.State)
	require.Empty(t, sc.CapturedOutput)
}

func TestVitest_ImportError_ClassifiedAsBuildError(t *testing.T) {
	run := javascript.ParseJestVitestJSON(realisticVitestImportError, 0)
	resp := run.ToProtoResponse("vitest", "", time.Second)

	require.Equal(t, runtimev0.TestRunResult_ERRORED, resp.Result.State,
		"file-level failureMessage with no assertions = file failed to load = ERRORED")
	require.Len(t, resp.Suites, 1)
	require.Len(t, resp.Suites[0].Cases, 1)

	c := resp.Suites[0].Cases[0]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED, c.State)
	require.Equal(t, "<file>", c.Name,
		"synthetic <file> case name signals this isn't a real test")
	require.Equal(t, runtimev0.TestFailureKind_TEST_FAILURE_KIND_BUILD_ERROR, c.Failure.Kind)
	require.Contains(t, c.Failure.Detail, "Cannot find module")
}

func TestVitest_TimeoutClassified(t *testing.T) {
	const json = `{
      "numTotalTests": 1,
      "testResults": [{
        "name": "/x/test.ts",
        "status": "failed",
        "startTime": 1, "endTime": 1,
        "assertionResults": [{
          "ancestorTitles": [],
          "title": "slow",
          "fullName": "slow",
          "status": "failed",
          "duration": 5000,
          "failureMessages": ["Error: Test exceeded timeout of 5000ms\n    at Function.run"]
        }]
      }]
    }`
	resp := javascript.ParseJestVitestJSON(json, 0).ToProtoResponse("vitest", "", time.Second)
	c := resp.Suites[0].Cases[0]
	require.Equal(t, runtimev0.TestFailureKind_TEST_FAILURE_KIND_TIMEOUT, c.Failure.Kind,
		"timeout markers in failureMessages must classify as TIMEOUT")
}

func TestVitest_OutputCappedAtMaxBytes(t *testing.T) {
	// Build a fail-message larger than the cap. JSON-encode the
	// payload so we don't have to escape every char by hand.
	huge := strings.Repeat("x", javascript.MaxCapturedOutputBytesPerCase+8*1024)
	jsonPayload := `{
      "numTotalTests": 1,
      "testResults": [{
        "name": "/x/big.test.ts",
        "status": "failed",
        "startTime": 1, "endTime": 2,
        "assertionResults": [{
          "ancestorTitles": [],
          "title": "fat",
          "fullName": "fat",
          "status": "failed",
          "duration": 1,
          "failureMessages": ["` + huge + `"]
        }]
      }]
    }`
	run := javascript.ParseJestVitestJSON(jsonPayload, 0)
	resp := run.ToProtoResponse("vitest", "", time.Second)

	c := resp.Suites[0].Cases[0]
	require.LessOrEqual(t, len(c.CapturedOutput), javascript.MaxCapturedOutputBytesPerCase+128,
		"captured_output respects the cap + small marker overhead")
	require.True(t, resp.Truncation.Happened,
		"truncation must surface — silent truncation is the bug we're avoiding")
	require.EqualValues(t, 1, resp.Truncation.TruncatedCases)
}

func TestVitest_LegacyFieldsMirrorStructured(t *testing.T) {
	run := javascript.ParseJestVitestJSON(realisticVitestMixed, 75.0)
	resp := run.ToProtoResponse("vitest", "unit", time.Second)

	require.EqualValues(t, 3, resp.TestsRun)
	require.EqualValues(t, 1, resp.TestsPassed)
	require.EqualValues(t, 1, resp.TestsFailed)
	require.EqualValues(t, 1, resp.TestsSkipped)
	require.InDelta(t, 75.0, resp.CoveragePct, 0.001)
	require.Equal(t, runtimev0.TestStatus_ERROR, resp.Status.State,
		"any failure flips legacy status to ERROR")
	require.Len(t, resp.Failures, 1,
		"failures list contains one entry per failed case")

	require.NotNil(t, resp.Coverage)
	require.Equal(t, "istanbul", resp.Coverage.RawArtifactFormat,
		"vitest emits istanbul-format coverage")
}

func TestVitest_Empty_GracefulNoPanic(t *testing.T) {
	run := javascript.ParseJestVitestJSON("", 0)
	resp := run.ToProtoResponse("vitest", "", 0)
	require.Empty(t, resp.Suites)
	require.EqualValues(t, 0, resp.Counts.Total)
}

func TestVitest_MalformedJSON_GracefulNoPanic(t *testing.T) {
	run := javascript.ParseJestVitestJSON("{not valid json", 0)
	resp := run.ToProtoResponse("vitest", "", 0)
	require.Empty(t, resp.Suites,
		"malformed JSON must produce an empty run, not panic")
}

// --- Helper --------------------------------------------------------

func caseByName(t *testing.T, suite *runtimev0.TestSuite) map[string]*runtimev0.TestCase {
	t.Helper()
	out := make(map[string]*runtimev0.TestCase, len(suite.Cases))
	for _, c := range suite.Cases {
		out[c.Name] = c
	}
	return out
}
