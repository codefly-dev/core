package resources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A service's go.mod is in its own directory or in one subdirectory (go-grpc
// keeps it in code/): the replace for a library is written where it is.
func TestGoModuleDir(t *testing.T) {
	write := func(parts ...string) {
		t.Helper()
		path := filepath.Join(parts...)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("module x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	flat := t.TempDir()
	write(flat, "go.mod")
	write(flat, "tools", "go.mod")
	if dir, err := goModuleDir(flat); err != nil || dir != flat {
		t.Fatalf("the service's own go.mod wins: %s %v", dir, err)
	}
	nested := t.TempDir()
	write(nested, "code", "go.mod")
	write(nested, ".cache", "go.mod")
	if dir, err := goModuleDir(nested); err != nil || dir != filepath.Join(nested, "code") {
		t.Fatalf("the one subdirectory with a go.mod: %s %v", dir, err)
	}
	if _, err := goModuleDir(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no go.mod") {
		t.Fatalf("no module is an error: %v", err)
	}
	two := t.TempDir()
	write(two, "a", "go.mod")
	write(two, "b", "go.mod")
	if _, err := goModuleDir(two); err == nil || !strings.Contains(err.Error(), "several") {
		t.Fatalf("two candidates are not guessed between: %v", err)
	}
}
