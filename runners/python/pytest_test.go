package python

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

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
