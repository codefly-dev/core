package javascript_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/runners/javascript"
)

// realisticPlaywrightPassing is a passing playwright run with one
// test in one file under one project. Shape pulled from real
// `--reporter=json` output.
const realisticPlaywrightPassing = `{
  "stats": {"startTime":"2026-04-30T20:00:00Z","duration":1234,"expected":1,"skipped":0,"unexpected":0,"flaky":0},
  "suites": [
    {
      "title": "chromium",
      "file": "",
      "suites": [
        {
          "title": "tests/login.spec.ts",
          "file": "tests/login.spec.ts",
          "specs": [
            {
              "title": "user logs in",
              "file": "tests/login.spec.ts",
              "line": 10,
              "tests": [
                {
                  "projectName": "chromium",
                  "expectedStatus": "passed",
                  "status": "expected",
                  "results": [
                    {"retry": 0, "status": "passed", "duration": 1234}
                  ]
                }
              ]
            }
          ]
        }
      ]
    }
  ]
}`

const realisticPlaywrightFailed = `{
  "stats": {"startTime":"2026-04-30T20:00:00Z","duration":2345,"expected":0,"skipped":0,"unexpected":1,"flaky":0},
  "suites": [
    {
      "title": "chromium",
      "specs": [],
      "suites": [
        {
          "title": "tests/checkout.spec.ts",
          "file": "tests/checkout.spec.ts",
          "specs": [
            {
              "title": "completes payment",
              "file": "tests/checkout.spec.ts",
              "line": 25,
              "tests": [
                {
                  "projectName": "chromium",
                  "expectedStatus": "passed",
                  "status": "unexpected",
                  "results": [
                    {
                      "retry": 0,
                      "status": "failed",
                      "duration": 2345,
                      "error": {
                        "message": "Locator.click: Timeout 5000ms exceeded",
                        "stack": "Error: Locator.click: Timeout 5000ms exceeded\n    at /abs/checkout.spec.ts:30:14"
                      }
                    }
                  ]
                }
              ]
            }
          ]
        }
      ]
    }
  ]
}`

const realisticPlaywrightFlaky = `{
  "stats": {"expected":1,"flaky":1},
  "suites": [
    {
      "title": "chromium",
      "specs": [],
      "suites": [
        {
          "title": "tests/api.spec.ts",
          "file": "tests/api.spec.ts",
          "specs": [
            {
              "title": "fetches",
              "file": "tests/api.spec.ts",
              "line": 5,
              "tests": [
                {
                  "projectName": "chromium",
                  "expectedStatus": "passed",
                  "status": "flaky",
                  "results": [
                    {
                      "retry": 0,
                      "status": "failed",
                      "duration": 1000,
                      "error": {"message": "Network error", "stack": "Error: Network error"}
                    },
                    {
                      "retry": 1,
                      "status": "passed",
                      "duration": 800
                    }
                  ]
                }
              ]
            }
          ]
        }
      ]
    }
  ]
}`

// --- Tests ---------------------------------------------------------

func TestPlaywright_PassingTest_NoCapturedOutput(t *testing.T) {
	run := javascript.ParsePlaywrightJSON(realisticPlaywrightPassing)
	resp := run.ToProtoResponse("playwright", "e2e", time.Second)

	require.Equal(t, runtimev0.TestRunResult_PASSED, resp.Result.State)
	require.EqualValues(t, 1, resp.Counts.Total)
	require.EqualValues(t, 1, resp.Counts.Passed)

	require.Len(t, resp.Suites, 1)
	require.Equal(t, "tests/login.spec.ts", resp.Suites[0].File)
	c := resp.Suites[0].Cases[0]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_PASSED, c.State)
	require.Empty(t, c.CapturedOutput,
		"PASSED case has no captured output — the load-bearing rule")
	require.NotNil(t, c.Location)
	require.EqualValues(t, 10, c.Location.Line)
	require.Empty(t, c.Retries, "no retries on a clean pass")
}

func TestPlaywright_FailedTest_TimeoutClassification(t *testing.T) {
	run := javascript.ParsePlaywrightJSON(realisticPlaywrightFailed)
	resp := run.ToProtoResponse("playwright", "e2e", time.Second)

	require.Equal(t, runtimev0.TestRunResult_FAILED, resp.Result.State)
	require.EqualValues(t, 1, resp.Counts.Failed)

	c := resp.Suites[0].Cases[0]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_FAILED, c.State)
	require.NotNil(t, c.Failure)
	require.Contains(t, c.Failure.Detail, "Locator.click",
		"failure.detail must contain the playwright error message")
	require.Contains(t, c.Failure.Detail, "Timeout 5000ms",
		"detail must include the runner's actual error wording")
	require.NotEmpty(t, c.CapturedOutput,
		"FAILED cases get the captured failure detail")
}

func TestPlaywright_FlakyTest_PassedAfterRetry_RetriesPopulated(t *testing.T) {
	run := javascript.ParsePlaywrightJSON(realisticPlaywrightFlaky)
	resp := run.ToProtoResponse("playwright", "e2e", time.Second)

	// Flaky = eventually passed → counts as PASSED at the run level.
	require.Equal(t, runtimev0.TestRunResult_PASSED, resp.Result.State)
	require.EqualValues(t, 1, resp.Counts.Passed)

	c := resp.Suites[0].Cases[0]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_PASSED, c.State,
		"flaky tests that eventually pass surface as PASSED in the case state")

	// But retries[] carries the failed attempts — consumers can flag
	// the test for quarantine even though it passed.
	require.Len(t, c.Retries, 1, "one earlier failed attempt before the passing retry")
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_FAILED, c.Retries[0].State)
	require.NotNil(t, c.Retries[0].Failure)
	require.Contains(t, c.Retries[0].Failure.Detail, "Network error")
	require.EqualValues(t, 1, c.Retries[0].Attempt,
		"playwright's retry=0 + 1-indexed attempt → first attempt is Attempt=1")
}

func TestPlaywright_NestedSuites_FlattenedToFiles(t *testing.T) {
	// Three projects (browsers) × two specs each in different files.
	// Cases must group by file, not by project — playwright
	// projects are a runtime concern, files are the user's mental
	// model.
	const xml = `{
      "stats": {"expected": 4, "unexpected": 0},
      "suites": [
        {
          "title": "chromium",
          "specs": [],
          "suites": [
            {
              "title": "tests/a.spec.ts",
              "file": "tests/a.spec.ts",
              "specs": [{"title":"specA","file":"tests/a.spec.ts","line":1,"tests":[{"projectName":"chromium","status":"expected","results":[{"retry":0,"status":"passed","duration":1}]}]}]
            },
            {
              "title": "tests/b.spec.ts",
              "file": "tests/b.spec.ts",
              "specs": [{"title":"specB","file":"tests/b.spec.ts","line":1,"tests":[{"projectName":"chromium","status":"expected","results":[{"retry":0,"status":"passed","duration":1}]}]}]
            }
          ]
        },
        {
          "title": "firefox",
          "specs": [],
          "suites": [
            {
              "title": "tests/a.spec.ts",
              "file": "tests/a.spec.ts",
              "specs": [{"title":"specA","file":"tests/a.spec.ts","line":1,"tests":[{"projectName":"firefox","status":"expected","results":[{"retry":0,"status":"passed","duration":1}]}]}]
            },
            {
              "title": "tests/b.spec.ts",
              "file": "tests/b.spec.ts",
              "specs": [{"title":"specB","file":"tests/b.spec.ts","line":1,"tests":[{"projectName":"firefox","status":"expected","results":[{"retry":0,"status":"passed","duration":1}]}]}]
            }
          ]
        }
      ]
    }`
	resp := javascript.ParsePlaywrightJSON(xml).ToProtoResponse("playwright", "", time.Second)

	require.Len(t, resp.Suites, 2,
		"two FILES (a.spec.ts, b.spec.ts) regardless of how many browsers ran")
	for _, s := range resp.Suites {
		require.Len(t, s.Cases, 2, "two browsers × one spec per file = two cases")
	}
}

func TestPlaywright_Empty_GracefulNoPanic(t *testing.T) {
	resp := javascript.ParsePlaywrightJSON("").ToProtoResponse("playwright", "", 0)
	require.Empty(t, resp.Suites)
}
