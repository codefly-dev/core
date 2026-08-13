package python

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// TestRunPythonTestsStructuredClassifiesInvalidTargetAsSelectionError is the
// real default-runner regression for a model-supplied unittest-style selector.
// Pytest exits non-zero without writing JUnit for this input. That is a caller
// selection miss, not an unavailable environment, and its typed diagnostic
// must remain stable across the runner's fresh temporary directories.
func TestRunPythonTestsStructuredClassifiesInvalidTargetAsSelectionError(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Fatalf("uv is required for the production Python runner: %v", err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test_calc.py"), []byte(`
class CalculatorTests:
    def test_subtract(self):
        assert 2 - 1 == 1
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	const target = "CalculatorTests.test_subtract"
	run, err := RunPythonTestsStructured(ctx, root, nil, TestOptions{Target: target, VerboseSet: true})
	if err != nil {
		t.Fatalf("RunPythonTestsStructured: %v\n%s", err, run.RawOutput)
	}
	if run.EnvError == nil || run.EnvError.Reason != EnvErrorNoTestsMatchedSelectors {
		t.Fatalf("invalid target classification = %+v, want %q\n%s", run.EnvError, EnvErrorNoTestsMatchedSelectors, run.RawOutput)
	}
	for _, volatile := range []string{"codefly-pytest-", "pytest-junit-", root} {
		if strings.Contains(run.EnvError.Detail, volatile) {
			t.Fatalf("stable selection diagnostic contains transient path %q: %s", volatile, run.EnvError.Detail)
		}
	}
	response := run.ToProtoResponse("pytest", "", 0)
	if message := response.GetResult().GetMessage(); !strings.HasPrefix(message, "test-selection-error (") || strings.Contains(message, "env-blocked") {
		t.Fatalf("typed runtime result = %q, want selection error", message)
	}
	if response.GetResult().GetState() != runtimev0.TestRunResult_ERRORED || response.GetCounts().GetTotal() != 0 {
		t.Fatalf("typed runtime result = %+v counts=%+v, want zero-case error", response.GetResult(), response.GetCounts())
	}
}

// TestRunPythonTestsStructuredMaterializesDeclaredRequirements is the real
// default-adapter proof: the test imports a separately packaged dependency
// that exists only through requirements.txt. The runner must ask uv to build
// that declared environment; ambient Python and a pytest-only overlay cannot
// make this pass.
func TestRunPythonTestsStructuredMaterializesDeclaredRequirements(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Fatalf("uv is required for the production Python runner: %v", err)
	}
	root := t.TempDir()
	dependencyDir := filepath.Join(root, "supportdep")
	if err := os.MkdirAll(dependencyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("supportdep/pyproject.toml", `[build-system]
requires = ["setuptools>=68"]
build-backend = "setuptools.build_meta"

[project]
name = "codefly-declared-probe-dependency"
version = "0.0.1"

[tool.setuptools]
py-modules = ["declared_probe_dependency"]
`)
	write("supportdep/declared_probe_dependency.py", "VALUE = 'from-declared-requirements'\n")
	write("requirements.txt", "./supportdep\n")
	write("test_declared_dependency.py", `import declared_probe_dependency

def test_dependency_was_materialized_from_project_declaration():
    assert declared_probe_dependency.VALUE == "from-declared-requirements"
`)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	run, err := RunPythonTestsStructured(ctx, root, nil, TestOptions{VerboseSet: true})
	if err != nil {
		t.Fatalf("RunPythonTestsStructured: %v\n%s", err, run.RawOutput)
	}
	if run.EnvError != nil {
		t.Fatalf("default adapter environment error: %s\n%s", run.EnvError.Detail, run.RawOutput)
	}
	summary := run.LegacyTestSummary()
	if summary.Run != 1 || summary.Passed != 1 || summary.Failed != 0 {
		t.Fatalf("summary = %+v, want one passed test\n%s", summary, run.RawOutput)
	}
	assertDefaultRunnerLeftSourceClean(t, root)
}

// TestRunPythonTestsStructuredMaterializesTimeoutAdapter proves the typed
// per-case timeout does not become a project dependency. The production
// adapter owns pytest-timeout and must add it to the isolated uv environment
// even when the project itself declares no such plugin.
func TestRunPythonTestsStructuredMaterializesTimeoutAdapter(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Fatalf("uv is required for the production Python runner: %v", err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test_timeout_adapter.py"), []byte(`def test_timeout_adapter_is_available():
    assert True
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	run, err := RunPythonTestsStructured(ctx, root, nil, TestOptions{VerboseSet: true, Timeout: "30s"})
	if err != nil {
		t.Fatalf("RunPythonTestsStructured: %v\n%s", err, run.RawOutput)
	}
	if run.EnvError != nil {
		t.Fatalf("timeout adapter environment error: %s\n%s", run.EnvError.Detail, run.RawOutput)
	}
	summary := run.LegacyTestSummary()
	if summary.Run != 1 || summary.Passed != 1 || summary.Failed != 0 {
		t.Fatalf("summary = %+v, want one passed test\n%s", summary, run.RawOutput)
	}
	assertDefaultRunnerLeftSourceClean(t, root)
}

// TestRunPythonTestsStructuredMaterializesDeclaredDependencyGroups proves the
// pyproject-backed default path uses uv's isolated project mode. The declared
// group must be available, while uv.lock and .venv remain absent from source.
func TestRunPythonTestsStructuredMaterializesDeclaredDependencyGroups(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Fatalf("uv is required for the production Python runner: %v", err)
	}
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pyproject.toml", `[project]
name = "codefly-declared-group-probe"
version = "0.0.1"

[dependency-groups]
test = ["boltons==24.0.0"]
`)
	write("test_declared_group.py", `import boltons

def test_dependency_group_was_materialized():
    assert boltons is not None
`)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	run, err := RunPythonTestsStructured(ctx, root, nil, TestOptions{VerboseSet: true})
	if err != nil {
		t.Fatalf("RunPythonTestsStructured: %v\n%s", err, run.RawOutput)
	}
	if run.EnvError != nil {
		t.Fatalf("default adapter environment error: %s\n%s", run.EnvError.Detail, run.RawOutput)
	}
	summary := run.LegacyTestSummary()
	if summary.Run != 1 || summary.Passed != 1 || summary.Failed != 0 {
		t.Fatalf("summary = %+v, want one passed test\n%s", summary, run.RawOutput)
	}
	assertDefaultRunnerLeftSourceClean(t, root)
}

// assertDefaultRunnerLeftSourceClean proves the default validation capability
// is observational: package materialization and test evidence belong in the
// runner's ephemeral state, never in the user's checkout.
func assertDefaultRunnerLeftSourceClean(t *testing.T, root string) {
	t.Helper()
	for _, generated := range []string{"uv.lock", ".venv", ".pytest_cache", "__pycache__"} {
		if _, err := os.Stat(filepath.Join(root, generated)); !os.IsNotExist(err) {
			t.Fatalf("production runner generated %s in source checkout", generated)
		}
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".egg-info") {
			t.Fatalf("production runner generated package metadata in source checkout: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect source checkout after test run: %v", err)
	}
}

// TestRunPythonTestsStructuredToleratesIrregularFilesInCheckout proves the
// read-only snapshot survives a checkout that contains an irregular file (a
// stray unix socket / FIFO — common under a live-dev tree). os.CopyFS aborts
// the whole copy on such a file; the default adapter must run the tests anyway
// and still leave the source clean.
func TestRunPythonTestsStructuredToleratesIrregularFilesInCheckout(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Fatalf("uv is required for the production Python runner: %v", err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test_ok.py"),
		[]byte("def test_ok():\n    assert True\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "runtime.sock"), 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	run, err := RunPythonTestsStructured(ctx, root, nil, TestOptions{VerboseSet: true})
	if err != nil {
		t.Fatalf("RunPythonTestsStructured: %v\n%s", err, run.RawOutput)
	}
	if run.EnvError != nil {
		t.Fatalf("default adapter environment error: %s\n%s", run.EnvError.Detail, run.RawOutput)
	}
	if summary := run.LegacyTestSummary(); summary.Run != 1 || summary.Passed != 1 {
		t.Fatalf("summary = %+v, want one passed test\n%s", summary, run.RawOutput)
	}
	assertDefaultRunnerLeftSourceClean(t, root)
}

// TestRunPythonTestsStructuredBuildsGitVersionedProject proves the read-only
// snapshot preserves .git so a build backend that derives its version from git
// (setuptools_scm) can build the project. Excluding .git failed the build with
// "unable to detect version ... not a git repository", env-erroring a run that
// should pass — while the checkout must still stay clean.
func TestRunPythonTestsStructuredBuildsGitVersionedProject(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Fatalf("uv is required for the production Python runner: %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required for this test: %v", err)
	}
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pyproject.toml", `[build-system]
requires = ["setuptools>=68", "setuptools_scm>=8"]
build-backend = "setuptools.build_meta"

[project]
name = "codefly-git-versioned-probe"
dynamic = ["version"]

[tool.setuptools_scm]

[tool.setuptools]
py-modules = ["git_versioned_probe"]
`)
	write("git_versioned_probe.py", "VALUE = 'from-git-versioned-build'\n")
	write("test_git_versioned.py", `import git_versioned_probe

def test_project_built_from_git_version():
    assert git_versioned_probe.VALUE == "from-git-versioned-build"
`)

	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@codefly.dev"},
		{"config", "user.name", "codefly test"},
		// Test repositories own their complete Git identity. Inheriting a
		// developer's global commit/tag signing policy makes this headless
		// fixture depend on an interactive key agent and fail before exercising
		// Python.
		{"config", "commit.gpgsign", "false"},
		{"config", "tag.gpgsign", "false"},
		{"add", "-A"},
		{"commit", "-m", "init"},
		{"tag", "-m", "release", "v1.2.3"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	run, err := RunPythonTestsStructured(ctx, root, nil, TestOptions{VerboseSet: true})
	if err != nil {
		t.Fatalf("RunPythonTestsStructured: %v\n%s", err, run.RawOutput)
	}
	if run.EnvError != nil {
		t.Fatalf("default adapter environment error: %s\n%s", run.EnvError.Detail, run.RawOutput)
	}
	if summary := run.LegacyTestSummary(); summary.Run != 1 || summary.Passed != 1 {
		t.Fatalf("summary = %+v, want one passed test\n%s", summary, run.RawOutput)
	}
	assertDefaultRunnerLeftSourceClean(t, root)
}

// TestSnapshotSourceTree exercises the snapshot copy directly: source files
// (including .git, which build backends may read) are reproduced and symlinks
// preserved, while regenerable virtualenv/cache directories, prior build
// metadata, and irregular files never enter the snapshot.
func TestSnapshotSourceTree(t *testing.T) {
	src := t.TempDir()
	write := func(p, c string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(src, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pkg/module.py", "VALUE = 1\n")
	write("requirements.txt", "./dep\n")
	write("uv.lock", "version = 1\n")
	// .git must be preserved: build backends like setuptools_scm read it at
	// build time to derive a dynamic version. Excluding it would fail the run.
	write(".git/config", "[core]\n")
	write(".venv/bin/python", "#!/bin/sh\n")
	write("pkg.egg-info/PKG-INFO", "Metadata-Version: 2.1\n")
	write("__pycache__/module.cpython-312.pyc", "bytecode\n")
	if err := os.Symlink("module.py", filepath.Join(src, "pkg", "link.py")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(src, "runtime.sock"), 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "snapshot")
	if err := snapshotSourceTree(src, dst); err != nil {
		t.Fatalf("snapshotSourceTree: %v", err)
	}

	present := func(p string) bool {
		_, err := os.Lstat(filepath.Join(dst, p))
		return err == nil
	}
	for _, p := range []string{"pkg/module.py", "requirements.txt", "uv.lock", ".git/config"} {
		if !present(p) {
			t.Errorf("snapshot missing source file %s", p)
		}
	}
	info, err := os.Lstat(filepath.Join(dst, "pkg", "link.py"))
	if err != nil {
		t.Errorf("snapshot missing symlink pkg/link.py: %v", err)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("snapshot did not preserve pkg/link.py as a symlink")
	}
	for _, p := range []string{".venv", "pkg.egg-info", "__pycache__", "runtime.sock"} {
		if present(p) {
			t.Errorf("snapshot copied excluded entry %s", p)
		}
	}
}

// TestSnapshotSourceTreeFollowsSymlinkedRoot proves the snapshot resolves a
// symlinked source root — a real deployment shape (see the symlinked-source-root
// handling in resource loading). filepath.WalkDir does not follow the root
// symlink; without resolution the snapshot would be a lone dangling symlink and
// the whole test run would execute in a non-existent directory.
func TestSnapshotSourceTreeFollowsSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "pkg", "module.py"), []byte("VALUE = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "snapshot")
	if err := snapshotSourceTree(link, dst); err != nil {
		t.Fatalf("snapshotSourceTree through symlinked root: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil || !info.IsDir() {
		t.Fatalf("snapshot root is not a real directory (info=%v err=%v)", info, err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "pkg", "module.py"))
	if err != nil {
		t.Fatalf("snapshot did not copy contents through symlinked root: %v", err)
	}
	if string(got) != "VALUE = 1\n" {
		t.Errorf("copied content = %q, want %q", got, "VALUE = 1\n")
	}
}

// TestScanPytestEvents_EmitsPerLine feeds realistic pytest verbose
// output through scanPytestEvents and asserts the callback fires once
// per progress line, in order.
func TestScanPytestEvents_EmitsPerLine(t *testing.T) {
	input := strings.NewReader(`============ test session starts ============
collected 4 items

tests/test_a.py::test_one PASSED                              [ 25%]
tests/test_a.py::test_two FAILED                              [ 50%]
tests/test_b.py::test_three SKIPPED (skipped reason)          [ 75%]
tests/test_b.py::test_four PASSED                             [100%]

============ short test summary ============
`)

	var mu sync.Mutex
	var got []TestEvent
	scanPytestEvents(input, func(ev TestEvent) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, ev)
	})

	mu.Lock()
	defer mu.Unlock()
	want := []TestEvent{
		{Action: "pass", Test: "tests/test_a.py::test_one"},
		{Action: "fail", Test: "tests/test_a.py::test_two"},
		{Action: "skip", Test: "tests/test_b.py::test_three"},
		{Action: "pass", Test: "tests/test_b.py::test_four"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("event[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestPytestNode_StripsTrailingProgressMarker confirms the parser
// trims the percentage marker pytest appends, so callbacks see clean
// node ids that match the on-disk paths.
func TestPytestNode_StripsTrailingProgressMarker(t *testing.T) {
	cases := []struct {
		line, marker, want string
	}{
		{"tests/test_x.py::test_y PASSED                              [ 25%]", " PASSED", "tests/test_x.py::test_y"},
		{"  prefix-spaces tests/x.py::z FAILED [50%]", " FAILED", "prefix-spaces tests/x.py::z"},
		{"unrelated", " PASSED", ""},
	}
	for _, c := range cases {
		if got := pytestNode(c.line, c.marker); got != c.want {
			t.Errorf("pytestNode(%q, %q) = %q, want %q", c.line, c.marker, got, c.want)
		}
	}
}

// TestWriteLastTestOutput_PersistsRaw mirrors the go-side helper: a
// single dump to <cacheDir>/last-test.txt, atomic write, no .tmp
// straggler.
func TestWriteLastTestOutput_PersistsRaw(t *testing.T) {
	dir := t.TempDir()
	raw := "============= test session starts =============\nFAILED tests/test_x.py::test_a\n"

	if err := writeLastTestOutput(dir, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "last-test.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != raw {
		t.Errorf("content mismatch")
	}
	if _, err := os.Stat(filepath.Join(dir, "last-test.txt.tmp")); err == nil {
		t.Error(".tmp should be renamed away after write")
	}
}

// TestWriteLastTestOutput_OverwritesPreviousRun ensures operators see
// the LATEST run after a re-run, not stale history from an older one.
func TestWriteLastTestOutput_OverwritesPreviousRun(t *testing.T) {
	dir := t.TempDir()
	if err := writeLastTestOutput(dir, "run-one\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeLastTestOutput(dir, "run-two\n"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "last-test.txt"))
	if string(got) != "run-two\n" {
		t.Errorf("got %q, want run-two", string(got))
	}
}

// TestWriteLastTestOutput_CreatesMissingDir confirms callers don't
// have to mkdir first — common when CacheDir lives under a fresh
// service tree.
func TestWriteLastTestOutput_CreatesMissingDir(t *testing.T) {
	deep := filepath.Join(t.TempDir(), "a", "b", "c")
	if err := writeLastTestOutput(deep, "x\n"); err != nil {
		t.Fatalf("write into nested missing dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(deep, "last-test.txt")); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

// TestCombinePytestK confirms multi-filter expansion uses pytest's
// boolean-expression idiom (" or "), not a regex pipe — pytest's -k
// parses the value as a Python expression, not a regex.
func TestCombinePytestK(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"test_login"}, "test_login"},
		{[]string{"test_login", "test_logout"}, "test_login or test_logout"},
		{[]string{"a", "b", "c"}, "a or b or c"},
	}
	for _, tc := range cases {
		if got := combinePytestK(tc.in); got != tc.want {
			t.Errorf("combinePytestK(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
