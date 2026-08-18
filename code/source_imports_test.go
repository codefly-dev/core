package code

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

func TestInspectGoSourceImportsUsesStdlibParser(t *testing.T) {
	root := t.TempDir()
	source := "package main\nimport (\"context\"; alias \"example.com/lib/sub\")\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := inspectGoSourceImports(t.Context(), LocalVFS{}, root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"context", "example.com/lib/sub"}
	if len(got) != 1 || got[0].GetPath() != "main.go" || !reflect.DeepEqual(got[0].GetImports(), want) {
		t.Fatalf("go source imports = %+v, want path main.go imports %#v", got, want)
	}
}

func TestInspectSourceImportsWithoutAnalyzerRejectsNonGo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("import os\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := NewDefaultCodeServer(root)
	if _, err := srv.inspectSourceImports(t.Context(), root, "python"); err == nil {
		t.Fatal("expected non-Go source import inspection to require a semantic analyzer")
	}
}

// A missing analyzer is a wiring gap, not malformed input: the project-info
// failure must be UNSUPPORTED_OPERATION so callers do not confuse it with a
// source syntax/validation error.
func TestNonGoProjectInfoWithoutAnalyzerIsUnsupported(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte("import os\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := NewPythonCodeServer(dir, nil)
	resp, err := srv.Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.GetFailure().GetCode(); got != basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION {
		t.Fatalf("failure code = %v, want FAILURE_CODE_UNSUPPORTED_OPERATION for a missing analyzer", got)
	}
}
