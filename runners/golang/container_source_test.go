package golang

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeSource(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func realTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// A playground composes a module from outside its own directory
// (`path: ../modules/...`, or a sibling checkout). The container mounts the
// workspace only, so the source, its module root and what its go.mod replaces
// from disk must each be named — and nothing wider than them.
func TestSourceOutsideTheWorkspaceIsMountedWithWhatItsModuleNeeds(t *testing.T) {
	root := realTempDir(t)
	workspace := filepath.Join(root, "playground")
	module := filepath.Join(root, "checkout", "services", "local", "code")
	library := filepath.Join(root, "checkout", "libraries", "hop")
	writeSource(t, filepath.Join(workspace, "workspace.codefly.yaml"), "name: playground\n")
	writeSource(t, filepath.Join(module, "go.mod"),
		"module example.com/local\n\ngo 1.21\n\nrequire (\n\texample.com/hop v0.0.0\n\texample.com/pinned v1.0.0\n)\n\n"+
			"replace example.com/hop => ../../../libraries/hop\n\n"+
			"replace example.com/pinned => example.com/fork v1.2.3\n\n"+
			"replace example.com/absent => ../../../libraries/absent\n")
	writeSource(t, filepath.Join(module, "cmd", "server", "main.go"), "package main\n\nfunc main() {}\n")
	writeSource(t, filepath.Join(library, "go.mod"), "module example.com/hop\n\ngo 1.21\n")

	source := containerSourceDir(workspace, filepath.Join(workspace, "..", "checkout", "services", "local", "code", "cmd", "server"))
	moduleDir, found := findGoModuleDir(t.Context(), workspace, source)
	if !found || moduleDir != module {
		t.Fatalf("module root = %q, %v; want %q", moduleDir, found, module)
	}
	dirs, err := sourceDirectories(source, moduleDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	mounts, err := sourceMounts(workspace, dirs)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{library, module}; !reflect.DeepEqual(mounts, want) {
		t.Fatalf("mounts = %q, want %q", mounts, want)
	}
	for _, mount := range mounts {
		if withinDir(mount, workspace) || withinDir(mount, root) {
			t.Fatalf("mount %q is wider than the source it serves", mount)
		}
	}
}

func TestSourceInsideTheWorkspaceNeedsNoMount(t *testing.T) {
	root := realTempDir(t)
	writeSource(t, filepath.Join(root, "services", "api", "code", "go.mod"),
		"module example.com/api\n\ngo 1.21\n\nrequire example.com/lib v0.0.0\n\nreplace example.com/lib => ../../../lib\n")
	writeSource(t, filepath.Join(root, "lib", "go.mod"), "module example.com/lib\n\ngo 1.21\n")
	source := containerSourceDir(root, filepath.Join(root, "services", "api", "code"))
	dirs, err := sourceDirectories(source, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	mounts, err := sourceMounts(root, dirs)
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 0 {
		t.Fatalf("the workspace mount already carries the source, got %q", mounts)
	}
}

// A workspace that reaches its source through a symlink has only the link
// inside the container. The source is named by its real path, where the
// relative directives in its go.mod mean what they mean on the host.
func TestSymlinkedSourceIsMountedAtItsRealPath(t *testing.T) {
	root := realTempDir(t)
	workspace := filepath.Join(root, "workspace")
	module := filepath.Join(root, "elsewhere", "module")
	writeSource(t, filepath.Join(module, "go.mod"), "module example.com/attached\n\ngo 1.21\n")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(module, filepath.Join(workspace, "code")); err != nil {
		t.Fatal(err)
	}
	source := containerSourceDir(workspace, filepath.Join(workspace, "code"))
	if source != module {
		t.Fatalf("source = %q, want the real path %q", source, module)
	}
	mounts, err := sourceMounts(workspace, []string{source})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{module}; !reflect.DeepEqual(mounts, want) {
		t.Fatalf("mounts = %q, want %q", mounts, want)
	}
}

// A source-owned go.work reads the sibling modules it uses and replaces even
// though nothing is executed in them.
func TestSourceOwnedWorkspaceNamesItsSiblingModules(t *testing.T) {
	root := realTempDir(t)
	workspace := filepath.Join(root, "playground")
	source := filepath.Join(root, "checkout", "service")
	sibling := filepath.Join(root, "sdk")
	replaced := filepath.Join(root, "fork")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSource(t, filepath.Join(source, "go.mod"), "module example.com/service\n\ngo 1.21\n")
	writeSource(t, filepath.Join(sibling, "go.mod"), "module example.com/sdk\n\ngo 1.21\n")
	writeSource(t, filepath.Join(replaced, "go.mod"), "module example.com/fork\n\ngo 1.21\n")
	writeSource(t, filepath.Join(source, "go.work"), "go 1.21\n\nuse (\n\t.\n\t../../sdk\n)\n\nreplace example.com/upstream => ../../fork\n")

	loaded, owned, err := loadSourceGoWorkspace(source)
	if err != nil || !owned {
		t.Fatalf("load go.work: owned=%v err=%v", owned, err)
	}
	dirs, err := sourceDirectories(source, loaded.root, loaded)
	if err != nil {
		t.Fatal(err)
	}
	mounts, err := sourceMounts(workspace, dirs)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{source, replaced, sibling}; !reflect.DeepEqual(mounts, want) {
		t.Fatalf("mounts = %q, want %q", mounts, want)
	}
}

// Making a build work is never worth handing a container the home directory.
func TestSourceMountsRefuseTheHomeDirectoryAndItsAncestors(t *testing.T) {
	home := realTempDir(t)
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "work", "playground")
	for _, dir := range []string{home, filepath.Dir(home), string(filepath.Separator)} {
		if _, err := sourceMounts(workspace, []string{dir}); err == nil {
			t.Errorf("%q was accepted as a source mount", dir)
		} else if !strings.Contains(err.Error(), "refusing to mount") {
			t.Errorf("%q: unexpected refusal %v", dir, err)
		}
	}
	inside := filepath.Join(home, "development", "checkout")
	mounts, err := sourceMounts(workspace, []string{inside})
	if err != nil || !reflect.DeepEqual(mounts, []string{inside}) {
		t.Fatalf("a checkout under home = %q, %v", mounts, err)
	}
	if _, err := sourceMounts(workspace, []string{"relative/source"}); err == nil {
		t.Fatal("a relative source directory was accepted")
	}
}
