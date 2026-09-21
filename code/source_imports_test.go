package code

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

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

// An analyzer-less server still knows its dependencies, packages and file
// hashes. Failing the whole inspection over the one piece of evidence it cannot
// produce would deny the caller everything else, so it declines just that piece
// and says so.
func TestNonGoProjectInfoWithoutAnalyzerOmitsSourceFiles(t *testing.T) {
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
	if got := resp.GetFailure(); got != nil {
		t.Fatalf("failure = %v, want project info to succeed without an analyzer", got)
	}
	info := resp.GetGetProjectInfo()
	if !info.GetSourceFilesOmitted() {
		t.Fatal("source_files_omitted is false, so an empty inventory reads as authoritative evidence that the project has no source files")
	}
	if got := info.GetSourceFiles(); len(got) != 0 {
		t.Fatalf("source_files = %v, want none alongside an omission", got)
	}
}

// The omission flag is a statement about the responder, not the project: a
// server that can inspect must never set it, or callers redo work that was
// already done authoritatively.
func TestGoProjectInfoNeverOmitsSourceFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nimport \"context\"\nvar _ = context.Background\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := NewGoCodeServer(dir, nil)
	resp, err := srv.Execute(t.Context(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_GetProjectInfo{GetProjectInfo: &codev0.GetProjectInfoRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	info := resp.GetGetProjectInfo()
	if info.GetSourceFilesOmitted() {
		t.Fatal("the stdlib-based Go inspector reported its inventory as not taken")
	}
	if len(info.GetSourceFiles()) == 0 {
		t.Fatal("go source inventory is empty")
	}
}
