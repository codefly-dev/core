package golang

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAttachedNestedSourceKeepsItsRealGoModule(t *testing.T) {
	ctx := t.Context()
	fixture, err := filepath.Abs("testdata/attached")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err = filepath.EvalSymlinks(fixture)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(fixture, "cmd", "probe"), filepath.Join(root, "code")); err != nil {
		t.Fatal(err)
	}
	module, found := findGoModuleDir(ctx, root, filepath.Join(root, "code"))
	if !found || module != fixture {
		t.Fatalf("attached module = %q, %v; want %q", module, found, fixture)
	}
	env, err := NewNativeGoRunner(ctx, root, "code")
	if err != nil {
		t.Fatal(err)
	}
	env.WithLocalCacheDir(t.TempDir())
	t.Cleanup(func() { _ = env.Shutdown(t.Context()) })
	if err := env.Init(ctx); err != nil {
		t.Fatal(err)
	}
	proc, err := env.Env().NewProcess("go", "run", ".")
	if err != nil {
		t.Fatal(err)
	}
	proc.WithDir(filepath.Join(root, "code"))
	var output bytes.Buffer
	proc.WithOutput(&output)
	if err := proc.Run(ctx); err != nil {
		t.Fatalf("attached package lost module imports: %v\n%s", err, output.String())
	}
	if output.String() != "attached-module\n" {
		t.Fatalf("unexpected module output: %q", output.String())
	}
}

func TestOrdinarySourceDoesNotAdoptModuleAboveWorkspace(t *testing.T) {
	parent := t.TempDir()
	if err := os.WriteFile(filepath.Join(parent, "go.mod"), []byte("module example.com/unrelated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "workspace")
	source := filepath.Join(root, "code")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if module, found := findGoModuleDir(t.Context(), root, source); found {
		t.Fatalf("ordinary source adopted unrelated module %s", module)
	}
}
