package code

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
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
