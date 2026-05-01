package golang_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/runners/golang"
)

// jsonEvents is a helper that builds the kind of `go test -json`
// output a real run produces. Each event is one line; we serialize
// inline rather than crafting fixture files so the assertions are
// next to the input.
func jsonEvents(events ...string) string {
	return strings.Join(events, "\n") + "\n"
}

func TestStructured_PassingCase_NoOutputCaptured(t *testing.T) {
	raw := jsonEvents(
		`{"Action":"run","Package":"pkg","Test":"TestFoo"}`,
		`{"Action":"output","Package":"pkg","Test":"TestFoo","Output":"=== RUN   TestFoo\n"}`,
		`{"Action":"output","Package":"pkg","Test":"TestFoo","Output":"--- PASS: TestFoo (0.01s)\n"}`,
		`{"Action":"pass","Package":"pkg","Test":"TestFoo","Elapsed":0.01}`,
		`{"Action":"pass","Package":"pkg","Elapsed":0.02}`,
	)
	run := golang.ParseTestJSONStructured(raw)
	resp := run.ToProtoResponse("go-test", "unit", time.Second)

	require.Equal(t, runtimev0.TestRunResult_PASSED, resp.Result.State)
	require.EqualValues(t, 1, resp.Counts.Total)
	require.EqualValues(t, 1, resp.Counts.Passed)
	require.EqualValues(t, 0, resp.Counts.Failed)

	require.Len(t, resp.Suites, 1)
	suite := resp.Suites[0]
	require.Equal(t, "pkg", suite.Name)
	require.Len(t, suite.Cases, 1)
	c := suite.Cases[0]
	require.Equal(t, "TestFoo", c.Name)
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_PASSED, c.State)

	// Load-bearing assertion: PASSED case has NO captured output.
	// This is the entire reason for the schema overhaul — a 10000-test
	// run that's all green should produce a tiny TestResponse.
	require.Empty(t, c.CapturedOutput,
		"PASSED cases must not carry their captured output (size discipline)")
	require.Nil(t, c.Failure,
		"PASSED cases have no failure detail")
}

func TestStructured_FailingCase_CarriesOutputAndFailureKind(t *testing.T) {
	raw := jsonEvents(
		`{"Action":"run","Package":"pkg","Test":"TestBar"}`,
		`{"Action":"output","Package":"pkg","Test":"TestBar","Output":"=== RUN   TestBar\n"}`,
		`{"Action":"output","Package":"pkg","Test":"TestBar","Output":"    bar_test.go:10: \n"}`,
		`{"Action":"output","Package":"pkg","Test":"TestBar","Output":"        \tError Trace:\tbar_test.go:10\n"}`,
		`{"Action":"output","Package":"pkg","Test":"TestBar","Output":"        \tError:      \tNot equal:\n"}`,
		`{"Action":"output","Package":"pkg","Test":"TestBar","Output":"        \t            \texpected: 3\n"}`,
		`{"Action":"output","Package":"pkg","Test":"TestBar","Output":"        \t            \tactual  : 5\n"}`,
		`{"Action":"output","Package":"pkg","Test":"TestBar","Output":"--- FAIL: TestBar (0.00s)\n"}`,
		`{"Action":"fail","Package":"pkg","Test":"TestBar","Elapsed":0.00}`,
		`{"Action":"fail","Package":"pkg","Elapsed":0.01}`,
	)
	run := golang.ParseTestJSONStructured(raw)
	resp := run.ToProtoResponse("go-test", "", time.Second)

	require.Equal(t, runtimev0.TestRunResult_FAILED, resp.Result.State)
	require.EqualValues(t, 1, resp.Counts.Failed)

	c := resp.Suites[0].Cases[0]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_FAILED, c.State)
	require.NotEmpty(t, c.CapturedOutput,
		"FAILED case must carry its captured output for diagnosis")
	require.Contains(t, c.CapturedOutput, "expected: 3")
	require.Contains(t, c.CapturedOutput, "actual  : 5")

	require.NotNil(t, c.Failure)
	require.Equal(t, runtimev0.TestFailureKind_TEST_FAILURE_KIND_ASSERTION, c.Failure.Kind,
		"plain assertion failure must be classified as ASSERTION")
	require.Contains(t, c.Failure.Detail, "expected: 3",
		"failure.detail must contain the assertion detail for IDE display")
}

func TestStructured_PanicCase_ClassifiedAsPanic(t *testing.T) {
	raw := jsonEvents(
		`{"Action":"run","Package":"pkg","Test":"TestPanics"}`,
		`{"Action":"output","Package":"pkg","Test":"TestPanics","Output":"--- FAIL: TestPanics (0.00s)\n"}`,
		`{"Action":"output","Package":"pkg","Test":"TestPanics","Output":"panic: runtime error: invalid memory address or nil pointer dereference\n"}`,
		`{"Action":"output","Package":"pkg","Test":"TestPanics","Output":"goroutine 5 [running]:\n"}`,
		`{"Action":"fail","Package":"pkg","Test":"TestPanics","Elapsed":0}`,
		`{"Action":"fail","Package":"pkg","Elapsed":0.01}`,
	)
	run := golang.ParseTestJSONStructured(raw)
	resp := run.ToProtoResponse("go-test", "", time.Second)

	c := resp.Suites[0].Cases[0]
	require.Equal(t, runtimev0.TestFailureKind_TEST_FAILURE_KIND_PANIC, c.Failure.Kind,
		"panic markers in output must classify as PANIC, not ASSERTION")
}

func TestStructured_BuildFailure_AttributedAsErroredAndBuildError(t *testing.T) {
	raw := jsonEvents(
		`{"Action":"start","Package":"pkg"}`,
		`{"Action":"build-output","Package":"pkg","Output":"./bar.go:10:5: undefined: Foo\n"}`,
		`{"Action":"build-fail","Package":"pkg"}`,
	)
	run := golang.ParseTestJSONStructured(raw)
	resp := run.ToProtoResponse("go-test", "", time.Second)

	require.Equal(t, runtimev0.TestRunResult_ERRORED, resp.Result.State,
		"build failure makes the WHOLE run ERRORED, not FAILED")
	require.EqualValues(t, 1, resp.Counts.Errored)

	require.Len(t, resp.Suites, 1)
	require.Len(t, resp.Suites[0].Cases, 1, "synthetic <package> case stands in for the whole package failure")
	c := resp.Suites[0].Cases[0]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_ERRORED, c.State)
	require.Equal(t, runtimev0.TestFailureKind_TEST_FAILURE_KIND_BUILD_ERROR, c.Failure.Kind)
	require.Contains(t, c.Failure.Detail, "undefined: Foo")
}

func TestStructured_SkippedCase_NoFailure(t *testing.T) {
	raw := jsonEvents(
		`{"Action":"run","Package":"pkg","Test":"TestSkipped"}`,
		`{"Action":"output","Package":"pkg","Test":"TestSkipped","Output":"--- SKIP: TestSkipped (0.00s)\n"}`,
		`{"Action":"skip","Package":"pkg","Test":"TestSkipped","Elapsed":0}`,
		`{"Action":"pass","Package":"pkg","Elapsed":0.01}`,
	)
	run := golang.ParseTestJSONStructured(raw)
	resp := run.ToProtoResponse("go-test", "", time.Second)

	require.Equal(t, runtimev0.TestRunResult_PASSED, resp.Result.State,
		"a skipped case alone doesn't fail the run")
	require.EqualValues(t, 1, resp.Counts.Skipped)

	c := resp.Suites[0].Cases[0]
	require.Equal(t, runtimev0.TestCaseState_TEST_CASE_STATE_SKIPPED, c.State)
	require.Empty(t, c.CapturedOutput,
		"SKIPPED case has no output captured — same retention rule as PASSED")
	require.Nil(t, c.Failure)
}

func TestStructured_LegacyFieldsArePopulated(t *testing.T) {
	// Backward compat: every legacy flat field must still carry the
	// right value, computed from the structured tree. Consumers that
	// haven't migrated read these and see no behavioral change.
	raw := jsonEvents(
		`{"Action":"run","Package":"pkg","Test":"TestA"}`,
		`{"Action":"pass","Package":"pkg","Test":"TestA","Elapsed":0.01}`,
		`{"Action":"run","Package":"pkg","Test":"TestB"}`,
		`{"Action":"output","Package":"pkg","Test":"TestB","Output":"--- FAIL: TestB\n"}`,
		`{"Action":"fail","Package":"pkg","Test":"TestB","Elapsed":0.02}`,
		`{"Action":"run","Package":"pkg","Test":"TestC"}`,
		`{"Action":"skip","Package":"pkg","Test":"TestC","Elapsed":0}`,
		`{"Action":"fail","Package":"pkg","Elapsed":0.03}`,
	)
	resp := golang.ParseTestJSONStructured(raw).ToProtoResponse("go-test", "unit", time.Second)

	require.EqualValues(t, 3, resp.TestsRun, "legacy TestsRun must reflect total")
	require.EqualValues(t, 1, resp.TestsPassed)
	require.EqualValues(t, 1, resp.TestsFailed)
	require.EqualValues(t, 1, resp.TestsSkipped)
	require.NotNil(t, resp.Status)
	require.Equal(t, runtimev0.TestStatus_ERROR, resp.Status.State,
		"any failure flips legacy status to ERROR (preserves existing consumer behavior)")

	require.Len(t, resp.Failures, 1, "legacy Failures string list must contain one entry per failed case")
	require.Contains(t, resp.Failures[0], "FAIL pkg/TestB")
}

func TestStructured_OutputCappedAtMaxBytes(t *testing.T) {
	// Build a fail case with >MaxCapturedOutputBytesPerCase bytes of output.
	huge := strings.Repeat("x", golang.MaxCapturedOutputBytesPerCase+1024)
	raw := jsonEvents(
		`{"Action":"run","Package":"pkg","Test":"TestHuge"}`,
		`{"Action":"output","Package":"pkg","Test":"TestHuge","Output":`+jsonString(huge)+`}`,
		`{"Action":"fail","Package":"pkg","Test":"TestHuge","Elapsed":0.01}`,
		`{"Action":"fail","Package":"pkg","Elapsed":0.02}`,
	)
	run := golang.ParseTestJSONStructured(raw)
	resp := run.ToProtoResponse("go-test", "", time.Second)

	c := resp.Suites[0].Cases[0]
	// The cap fires + truncation marker is appended; total stays at the cap.
	require.LessOrEqual(t, len(c.CapturedOutput), golang.MaxCapturedOutputBytesPerCase+128,
		"captured output must respect MaxCapturedOutputBytesPerCase + small marker overhead")

	require.NotNil(t, resp.Truncation)
	require.True(t, resp.Truncation.Happened,
		"truncation must surface when it fires — silent truncation is the bug we're avoiding")
	require.EqualValues(t, 1, resp.Truncation.TruncatedCases)
	require.EqualValues(t, golang.MaxCapturedOutputBytesPerCase, resp.Truncation.MaxPerCaseBytes)
}

func TestStructured_CoverageScrapedFromOutput(t *testing.T) {
	raw := jsonEvents(
		`{"Action":"run","Package":"pkg","Test":"TestA"}`,
		`{"Action":"pass","Package":"pkg","Test":"TestA","Elapsed":0.01}`,
		`{"Action":"output","Package":"pkg","Output":"coverage: 87.5% of statements\n"}`,
		`{"Action":"pass","Package":"pkg","Elapsed":0.02}`,
	)
	resp := golang.ParseTestJSONStructured(raw).ToProtoResponse("go-test", "", time.Second)

	require.NotNil(t, resp.Coverage, "coverage block must be set when scraped")
	require.InDelta(t, 87.5, resp.Coverage.TotalPct, 0.01)
	require.Equal(t, "go-cover", resp.Coverage.RawArtifactFormat)
	// Legacy field must mirror.
	require.InDelta(t, 87.5, resp.CoveragePct, 0.01)
}

func TestStructured_StableSuiteAndCaseOrder(t *testing.T) {
	// Determinism is load-bearing for review: alphabetical order across
	// runs makes test-output diffs reflect content changes, not map
	// iteration order.
	raw := jsonEvents(
		`{"Action":"run","Package":"zzz","Test":"TestZ"}`,
		`{"Action":"pass","Package":"zzz","Test":"TestZ","Elapsed":0.01}`,
		`{"Action":"pass","Package":"zzz","Elapsed":0.02}`,
		`{"Action":"run","Package":"aaa","Test":"TestB"}`,
		`{"Action":"pass","Package":"aaa","Test":"TestB","Elapsed":0.01}`,
		`{"Action":"run","Package":"aaa","Test":"TestA"}`,
		`{"Action":"pass","Package":"aaa","Test":"TestA","Elapsed":0.01}`,
		`{"Action":"pass","Package":"aaa","Elapsed":0.03}`,
	)
	resp := golang.ParseTestJSONStructured(raw).ToProtoResponse("go-test", "", time.Second)

	require.Len(t, resp.Suites, 2)
	require.Equal(t, "aaa", resp.Suites[0].Name, "suites must be alphabetical regardless of arrival order")
	require.Equal(t, "zzz", resp.Suites[1].Name)

	require.Len(t, resp.Suites[0].Cases, 2)
	require.Equal(t, "TestA", resp.Suites[0].Cases[0].Name, "cases must be alphabetical too")
	require.Equal(t, "TestB", resp.Suites[0].Cases[1].Name)
}

// jsonString quotes s as a JSON string (escapes the way go test -json
// would when emitting a payload). We don't use encoding/json here
// because it would add a top-level array; we just need the value-form.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
