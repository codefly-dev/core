package python

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

func TestNormalizeDiagnosticsRemovesOnlyRuntimeOwnedIdentities(t *testing.T) {
	root := "/var/folders/aa/bb/T/mind-ingest-cache/worktrees/project/session-matrix.repository-worktree-11111111-2222-3333-4444-555555555555"
	diagnostic := "self = <test_mod.TestThing object at 0x10835df60>\n" +
		"pytester = <Pytester PosixPath('/private/var/folders/aa/bb/T/pytest-of-operator/pytest-17/test_case0')>\n" +
		"/private" + root + "/testing/test_mark.py:587: AssertionError\n" +
		"assert 0x10 == 0x20"
	run := &StructuredTestRun{
		RawOutput: diagnostic,
		EnvError:  &RunEnvError{Reason: "assertion", Detail: diagnostic},
		Suites: []*StructuredSuite{{
			Name: root + "/testing/test_mark.py", File: root + "/testing/test_mark.py",
			Cases: []*StructuredCase{{
				Name: "test_case", FullName: "test_mod.test_case", File: root + "/testing/test_mark.py",
				State: runtimev0.TestCaseState_TEST_CASE_STATE_FAILED, Output: diagnostic,
				Failure: &StructuredFailure{Message: "object at 0x10835df60", Detail: diagnostic},
			}},
		}},
	}
	run.normalizeDiagnostics(root)
	response := run.ToProtoResponse("pytest", "", 0)
	body := strings.Join([]string{
		run.RawOutput, run.EnvError.Detail, response.GetOutput(),
		response.GetSuites()[0].GetFile(), response.GetSuites()[0].GetCases()[0].GetCapturedOutput(),
		response.GetSuites()[0].GetCases()[0].GetFailure().GetDetail(),
	}, "\n")
	for _, forbidden := range []string{root, "/private" + root, "pytest-17", "0x10835df60"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("normalized diagnostics retained %q:\n%s", forbidden, body)
		}
	}
	for _, required := range []string{"<workspace>/testing/test_mark.py:587", "<pytest-tmp>/test_case0", "object at 0xADDR", "assert 0x10 == 0x20"} {
		if !strings.Contains(body, required) {
			t.Fatalf("normalized diagnostics omitted %q:\n%s", required, body)
		}
	}
}

func TestRunFormulaStructuredReturnsStableFailureDiagnostics(t *testing.T) {
	requireUv(t)
	runFailure := func() string {
		t.Helper()
		root := t.TempDir()
		body := `def test_runtime_identity(tmp_path):
    class Subject:
        pass
    assert (Subject(), tmp_path) == ("expected", "expected")
`
		if err := os.WriteFile(filepath.Join(root, "test_identity.py"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		spec := SpecFromFormula(
			[]string{"pytest"}, OutputJUnitXML, nil,
			map[string]string{"no_project": "true"},
			[]string{"test_identity.py::test_runtime_identity"},
		)
		run, err := RunFormulaStructured(context.Background(), root, spec)
		if err != nil {
			t.Fatal(err)
		}
		response := run.ToProtoResponse("formula", "", 0)
		if response.GetCounts().GetFailed() != 1 {
			t.Fatalf("failure run = %+v\n%s", response.GetResult(), run.RawOutput)
		}
		return strings.Join([]string{
			response.GetOutput(),
			strings.Join(response.GetFailures(), "\n"),
			response.GetSuites()[0].GetCases()[0].GetCapturedOutput(),
		}, "\n")
	}

	first := runFailure()
	second := runFailure()
	if first != second {
		t.Fatalf("identical failures produced unstable diagnostics:\nFIRST:\n%s\nSECOND:\n%s", first, second)
	}
	for _, required := range []string{"0xADDR", "<pytest-tmp>"} {
		if !strings.Contains(first, required) {
			t.Fatalf("stable diagnostic omitted %q:\n%s", required, first)
		}
	}
}
