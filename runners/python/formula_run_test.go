package python

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// BuildUvArgs is the data→command translation. These tests prove the command
// AND the per-instance provisioning are DATA — nothing is hardcoded per
// framework. The SAME builder renders a pytest formula and a django formula;
// only the input data differs.

func TestBuildUvArgs_PytestFormula(t *testing.T) {
	got := strings.Join(BuildUvArgs(TestFormulaSpec{
		Command:   []string{"pytest"},
		Selectors: []string{"tests/test_a.py::test_x"},
		Output:    OutputJUnitXML,
	}, "/tmp/j.xml"), " ")
	want := "run pytest --junitxml=/tmp/j.xml tests/test_a.py::test_x"
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
}

func TestBuildUvArgs_DjangoFormula_NoJunit(t *testing.T) {
	// A django formula: the inner command comes from the project, output is
	// unittest-text (no --junitxml), selectors are dotted labels. The builder
	// has zero django knowledge — it just renders the data.
	got := strings.Join(BuildUvArgs(TestFormulaSpec{
		Command:   []string{"python", "tests/runtests.py", "--verbosity=2"},
		Selectors: []string{"model_fields", "auth_tests"},
		Output:    OutputUnittestText,
	}, ""), " ")
	want := "run python tests/runtests.py --verbosity=2 model_fields auth_tests"
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
}

func TestBuildUvArgs_ProvisioningIsData(t *testing.T) {
	// The SWE-bench per-instance provisioning (python pin, editable install,
	// requirement files, extra deps) flows as DATA into uv flags — this is what
	// the brain used to hardcode. The builder injects exactly what it's given.
	got := strings.Join(BuildUvArgs(TestFormulaSpec{
		NoProject:        true,
		Python:           "3.9",
		ExcludeNewer:     "2022-07-27T14:44:33Z",
		Editable:         true,
		Requirements:     []string{"requirements/tests.txt"},
		With:             []string{"tox<4", "setuptools"},
		DependencyGroups: []string{"dev"},
		Extras:           []string{"test"},
		Command:          []string{"pytest"},
		Selectors:        []string{"tests/test_x.py::test_y"},
		Output:           OutputJUnitXML,
	}, "/tmp/j.xml"), " ")
	want := "run --no-project --python 3.9 --managed-python --exclude-newer 2022-07-27T14:44:33Z --with-editable . " +
		"--with-requirements requirements/tests.txt --with tox<4 --with setuptools " +
		"--group dev --extra test " +
		"pytest --junitxml=/tmp/j.xml tests/test_x.py::test_y"
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
}

// SpecFromFormula translates a LANGUAGE-AGNOSTIC formula (generic command +
// opaque provisioning map) into the uv spec — the one place uv knowledge lives.
// This proves the brain can stay framework/toolchain-blind: it ships a map, the
// plugin turns the keys it understands into uv flags.
func TestSpecFromFormula_ProvisioningMapToUv(t *testing.T) {
	spec := SpecFromFormula(
		[]string{"pytest"},
		OutputJUnitXML,
		map[string]string{"PYTHONDONTWRITEBYTECODE": "1"},
		map[string]string{
			"no_project":        "true",
			"python":            "3.9",
			"editable":          "true",
			"with":              "tox<4, setuptools",
			"requirements":      "requirements/tests.txt",
			"dependency_groups": "dev",
			"extras":            "test",
		},
		[]string{"tests/test_x.py::test_y"},
	)
	got := strings.Join(BuildUvArgs(spec, "/tmp/j.xml"), " ")
	want := "run --no-project --python 3.9 --managed-python --with-editable . " +
		"--with-requirements requirements/tests.txt --with tox<4 --with setuptools " +
		"--with pytest --group dev --extra test " +
		"pytest --junitxml=/tmp/j.xml tests/test_x.py::test_y"
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	if len(spec.Env) != 1 || spec.Env[0].Key != "PYTHONDONTWRITEBYTECODE" {
		t.Fatalf("env not carried: %+v", spec.Env)
	}
	if !spec.PersistentVenv {
		t.Fatal("declared groups/extras must select one materialized environment")
	}
}

// The production CLI/Mind path derives a pytest formula from project
// declarations, then runs it inside uv --no-project. The runner is part of the
// adapter contract, so it must be provisioned even when the project itself has
// no test dependency group. This executes a real uv + pytest run; no mock or
// host-global pytest is involved.
func TestSpecFromFormulaMaterializesJUnitRunner(t *testing.T) {
	requireUv(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test_sample.py"), []byte("def test_truth():\n    assert True\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := SpecFromFormula(
		[]string{"pytest", "-v"},
		OutputJUnitXML,
		nil,
		map[string]string{"no_project": "true"},
		[]string{"test_sample.py::test_truth"},
	)
	if !containsRequirement(spec.With, "pytest") {
		t.Fatalf("JUnit adapter dependencies = %v, want pytest", spec.With)
	}
	run, err := RunFormulaStructured(context.Background(), root, spec)
	if err != nil {
		t.Fatal(err)
	}
	if run.EnvError != nil {
		t.Fatalf("JUnit adapter did not materialize: %+v\n%s", run.EnvError, run.RawOutput)
	}
	response := run.ToProtoResponse("formula", "", 0)
	if response.GetResult().GetState() != runtimev0.TestRunResult_PASSED || response.GetCounts().GetPassed() != 1 {
		t.Fatalf("result = %+v, counts = %+v\n%s", response.GetResult(), response.GetCounts(), run.RawOutput)
	}
}

func TestSpecFromFormulaKeepsExplicitJUnitRunnerConstraint(t *testing.T) {
	spec := SpecFromFormula(
		[]string{"pytest"},
		OutputJUnitXML,
		nil,
		map[string]string{"no_project": "true", "with": "Py_Test_Helper,PyTest<9"},
		nil,
	)
	if got := strings.Join(spec.With, ","); got != "Py_Test_Helper,PyTest<9" {
		t.Fatalf("explicit runner constraint was not authoritative: %q", got)
	}
}

func TestBuildUvArgs_EmptyDefaults(t *testing.T) {
	// Minimal formula: no provisioning, no junit (unittest-text) → just the
	// command. No spurious flags.
	got := strings.Join(BuildUvArgs(TestFormulaSpec{
		Command: []string{"pytest"},
		Output:  OutputUnittestText,
	}, ""), " ")
	if got != "run pytest" {
		t.Fatalf("got %q, want %q", got, "run pytest")
	}
}

func TestVerboseUVArgsKeepsDiagnosticFlagOutsideProjectCommand(t *testing.T) {
	got := strings.Join(verboseUVArgs([]string{"run", "--no-project", "python", "runtests.py"}), " ")
	if got != "run --verbose --no-project python runtests.py" {
		t.Fatalf("verbose uv args = %q", got)
	}
}

// A real uv build-backend failure observed during the Django headless session
// returned only "The build backend returned an error" on the normal path. The
// unhappy path must retain the backend exception that tells a remediator what
// to repair, while the normal successful path remains non-verbose.
func TestRunFormulaStructuredRecoversOpaqueBuildBackendDiagnostic(t *testing.T) {
	requireUv(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(`[build-system]
requires = []
build-backend = "broken_backend"
backend-path = ["."]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken_backend.py"), []byte(`def get_requires_for_build_editable(config_settings=None):
    raise RuntimeError("CODEFLY_VERBOSE_BACKEND_DIAGNOSTIC")
`), 0o644); err != nil {
		t.Fatal(err)
	}

	run, err := RunFormulaStructured(context.Background(), root, TestFormulaSpec{
		Command:   []string{"python", "-c", "pass"},
		Output:    OutputUnittestText,
		NoProject: true,
		Editable:  true,
	})
	if err != nil {
		t.Fatalf("structured failure must remain an operation result: %v", err)
	}
	if run.EnvError == nil || run.EnvError.Reason != "build-failed" {
		t.Fatalf("environment classification = %+v", run.EnvError)
	}
	if !strings.Contains(run.RawOutput, "CODEFLY_VERBOSE_BACKEND_DIAGNOSTIC") {
		t.Fatalf("verbose retry dropped backend exception:\n%s", run.RawOutput)
	}
}

// cwd is provisioning DATA (where to run), not a uv flag: SpecFromFormula must
// carry it into the spec, and BuildUvArgs must NOT render it.
func TestSpecFromFormula_CwdIsDataNotAUvFlag(t *testing.T) {
	spec := SpecFromFormula(
		[]string{"python", "runtests.py"},
		OutputUnittestText,
		nil,
		map[string]string{"cwd": "tests"},
		nil,
	)
	if spec.Cwd != "tests" {
		t.Fatalf("Cwd = %q, want %q", spec.Cwd, "tests")
	}
	got := strings.Join(BuildUvArgs(spec, ""), " ")
	// runtests.py gains --keepdb (django DB reuse); cwd must NOT appear in uv args.
	if got != "run python runtests.py --keepdb" {
		t.Fatalf("cwd must not leak into uv args, got %q", got)
	}
}

func TestResolveFormulaRunDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Run("empty stays at source dir", func(t *testing.T) {
		dir, err := resolveFormulaRunDir(root, "")
		if err != nil || dir != root {
			t.Fatalf("dir=%q err=%v, want %q", dir, err, root)
		}
	})
	t.Run("relative subdir resolves", func(t *testing.T) {
		dir, err := resolveFormulaRunDir(root, "tests")
		if err != nil || dir != filepath.Join(root, "tests") {
			t.Fatalf("dir=%q err=%v", dir, err)
		}
	})
	t.Run("dot-escape is rejected", func(t *testing.T) {
		if _, err := resolveFormulaRunDir(root, "../outside"); err == nil {
			t.Fatal("expected an escape rejection")
		}
		if _, err := resolveFormulaRunDir(root, "tests/../.."); err == nil {
			t.Fatal("expected a cleaned escape rejection")
		}
	})
	t.Run("absolute is rejected", func(t *testing.T) {
		if _, err := resolveFormulaRunDir(root, root); err == nil {
			t.Fatal("expected absolute cwd rejection")
		}
	})
	t.Run("missing dir is rejected", func(t *testing.T) {
		if _, err := resolveFormulaRunDir(root, "nope"); err == nil {
			t.Fatal("expected missing-dir rejection")
		}
	})
}

// THE core defect from the failed SWE-bench django run: a healed command that
// executes NOTHING (bare `python`, exit 0) must NEVER classify as a clean run.
// Real uv + real python — no mocks.
func TestRunFormulaStructured_ZeroTestsExecutedIsEnvError(t *testing.T) {
	requireUv(t)
	run, err := RunFormulaStructured(context.Background(), t.TempDir(), TestFormulaSpec{
		Command:   []string{"python", "-c", "pass"},
		Output:    OutputUnittestText,
		NoProject: true,
	})
	if err != nil {
		t.Fatalf("structural classification should not surface a raw error: %v", err)
	}
	if run.EnvError == nil {
		t.Fatal("zero cases + exit 0 must be an env error, not a clean run")
	}
	if run.EnvError.Reason != EnvErrorNoTestsExecuted {
		t.Fatalf("reason = %q, want %q", run.EnvError.Reason, EnvErrorNoTestsExecuted)
	}
	if !strings.Contains(run.EnvError.Detail, "exit status 0") {
		t.Fatalf("detail must name the treacherous exit 0, got %q", run.EnvError.Detail)
	}
	resp := run.ToProtoResponse("formula", "", 0)
	if resp.GetResult().GetState() != runtimev0.TestRunResult_ERRORED {
		t.Fatalf("zero-tests run must be ERRORED, got %v", resp.GetResult().GetState())
	}
}

// Selectors that match nothing classify DISTINCTLY (the command may be fine,
// the selection is wrong) so healers/callers can tell the two apart.
func TestRunFormulaStructured_ZeroTestsMatchedSelectors(t *testing.T) {
	requireUv(t)
	run, err := RunFormulaStructured(context.Background(), t.TempDir(), TestFormulaSpec{
		Command:   []string{"python", "-c", "pass"},
		Output:    OutputUnittestText,
		NoProject: true,
		Selectors: []string{"tests/test_missing.py::test_nope"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.EnvError == nil || run.EnvError.Reason != EnvErrorNoTestsMatchedSelectors {
		t.Fatalf("want %q env error, got %+v", EnvErrorNoTestsMatchedSelectors, run.EnvError)
	}
	if !strings.Contains(run.EnvError.Detail, "test_nope") {
		t.Fatalf("detail must name the unmatched selectors, got %q", run.EnvError.Detail)
	}
}

// The formula's cwd moves the RUN directory: a unittest module that is only
// importable from tests/ must pass with cwd=tests and env-block without it.
func TestRunFormulaStructured_CwdMovesRunDirectory(t *testing.T) {
	requireUv(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	testFile := "import unittest\n\nclass SampleTest(unittest.TestCase):\n    def test_truth(self):\n        self.assertTrue(True)\n"
	if err := os.WriteFile(filepath.Join(root, "tests", "test_sample.py"), []byte(testFile), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := TestFormulaSpec{
		Command:   []string{"python", "-m", "unittest", "-v", "test_sample"},
		Output:    OutputUnittestText,
		NoProject: true,
		Cwd:       "tests",
	}
	run, err := RunFormulaStructured(context.Background(), root, spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.EnvError != nil {
		t.Fatalf("cwd-run must execute the test, got env error %+v\nraw: %s", run.EnvError, run.RawOutput)
	}
	if run.caseCount() != 1 {
		t.Fatalf("expected 1 executed case, got %d\nraw: %s", run.caseCount(), run.RawOutput)
	}
	if _, err := os.Stat(filepath.Join(root, "tests", "__pycache__")); !os.IsNotExist(err) {
		t.Fatalf("formula run generated source-tree bytecode cache: %v", err)
	}

	// Same formula WITHOUT cwd: test_sample is not importable from the repo
	// root — unittest surfaces it as a loader-error case (or nothing at all).
	// Either way the run must NOT read as green.
	spec.Cwd = ""
	run, err = RunFormulaStructured(context.Background(), root, spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := run.ToProtoResponse("formula", "", 0)
	if state := resp.GetResult().GetState(); state != runtimev0.TestRunResult_ERRORED {
		t.Fatalf("root-run must be ERRORED (wrong cwd), got %v\nraw: %s", state, run.RawOutput)
	}
	if resp.GetCounts().GetPassed() != 0 {
		t.Fatalf("root-run must pass nothing, got %+v", resp.GetCounts())
	}
}

// An invalid cwd is an env error the healer can act on — never a silent
// fallback to the source dir.
func TestRunFormulaStructured_InvalidCwdIsEnvError(t *testing.T) {
	run, err := RunFormulaStructured(context.Background(), t.TempDir(), TestFormulaSpec{
		Command:   []string{"python", "-c", "pass"},
		Output:    OutputUnittestText,
		NoProject: true,
		Cwd:       "../escape",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.EnvError == nil || run.EnvError.Reason != EnvErrorInvalidCwd {
		t.Fatalf("want %q env error, got %+v", EnvErrorInvalidCwd, run.EnvError)
	}
}

// requireUv FAILS (never skips) when uv is unavailable — infra down means the
// suite is broken, and these tests exist to exercise the REAL runner.
func requireUv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("uv"); err != nil {
		t.Fatalf("uv is required for formula runner tests (install uv): %v", err)
	}
}

// Space-separated provisioning lists (healer LLMs write them) must split into
// individual specs — uv fails parsing "numpy>=1.14 scipy>=0.19" as ONE spec.
func TestSplitCommaAcceptsWhitespaceSeparators(t *testing.T) {
	got := splitComma("numpy>=1.14.0 scipy>=0.19.1 cython>=0.28.5")
	if len(got) != 3 || got[0] != "numpy>=1.14.0" || got[2] != "cython>=0.28.5" {
		t.Fatalf("split = %v, want 3 specs", got)
	}
	mixed := splitComma("a>=1, b==2 c<3")
	if len(mixed) != 3 {
		t.Fatalf("mixed separators = %v, want 3", mixed)
	}
}

// With Cwd moving the run dir (django tests/), the editable install must
// still target the PROJECT ROOT — a bare "." would point at the run dir.
func TestBuildUvArgsEditableTargetsProjectRootNotRunDir(t *testing.T) {
	spec := TestFormulaSpec{
		Command:        []string{"python", "runtests.py"},
		Editable:       true,
		EditableTarget: "/abs/project/root",
		Cwd:            "tests",
	}
	args := BuildUvArgs(spec, "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--with-editable /abs/project/root") {
		t.Fatalf("args = %q, want editable to target the project root", joined)
	}
}

// A budget-interrupted run that started executing tests (django's "Creating
// test database" appears) is MATERIALIZED (healthy), not an env block — the
// exact false-block that made django's 7757-test pre-warm read as failed.
func TestRunFormulaStructured_BudgetInterruptedAfterExecutionIsMaterialized(t *testing.T) {
	dir := t.TempDir()
	// A command that prints django's runner banner then sleeps forever, so the
	// ctx deadline (not the process) ends it after execution has started.
	script := "import sys; print('Creating test database for alias \\'default\\'...'); sys.stdout.flush(); import time; time.sleep(60)"
	spec := TestFormulaSpec{Command: []string{"python", "-c", script}, Output: OutputUnittestText, NoProject: true}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	run, err := RunFormulaStructured(ctx, dir, spec)
	if err != nil {
		t.Fatalf("RunFormulaStructured: %v", err)
	}
	if !run.Materialized {
		t.Fatalf("expected Materialized=true (runner launched, ctx-cut), got EnvError=%+v raw=%q", run.EnvError, run.RawOutput)
	}
	if run.EnvError != nil {
		t.Fatalf("materialized run must not carry an env block, got %+v", run.EnvError)
	}
	resp := run.ToProtoResponse("formula", "", 0)
	if !IsEnvironmentMaterializedMessage(resp.GetResult().GetMessage()) {
		t.Fatalf("proto message not marked materialized: %q", resp.GetResult().GetMessage())
	}
}

// A run that produces NO execution markers before being cut is a genuine block
// (build/import failed), not materialized.
func TestRunFormulaStructured_BudgetInterruptedBeforeExecutionIsNotMaterialized(t *testing.T) {
	dir := t.TempDir()
	script := "import time; time.sleep(60)" // no runner output at all
	spec := TestFormulaSpec{Command: []string{"python", "-c", script}, Output: OutputUnittestText, NoProject: true}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	run, err := RunFormulaStructured(ctx, dir, spec)
	if err != nil {
		t.Fatalf("RunFormulaStructured: %v", err)
	}
	if run.Materialized {
		t.Fatalf("no execution markers → must NOT be materialized")
	}
}

// django runtests.py needs DOTTED LABELS, not file paths — a workspace path
// selector made django ignore it and run the whole suite (8-12 min/call, the
// real reason django timed out). pytest paths pass through unchanged.
func TestSelectorsForCommand_DjangoLabelTranslation(t *testing.T) {
	got := selectorsForCommand([]string{"python", "runtests.py"}, "tests", []string{"tests/admin_docs/test_utils.py"})
	if len(got) != 1 || got[0] != "admin_docs.test_utils" {
		t.Fatalf("django selector = %v, want [admin_docs.test_utils]", got)
	}
	// unittest's verbose output identifies a case as `method (module.Class)`,
	// but the loader accepts only the inverse dotted label. SWE-bench and other
	// result-driven callers legitimately retain that structured display id.
	if g := selectorsForCommand(
		[]string{"python", "runtests.py"},
		"tests",
		[]string{"test_autoescape_off (template_tests.test_engine.RenderToStringTest)"},
	); g[0] != "template_tests.test_engine.RenderToStringTest.test_autoescape_off" {
		t.Fatalf("unittest display selector not normalized: %v", g)
	}
	// Already-dotted label passes through.
	if g := selectorsForCommand([]string{"runtests.py"}, "tests", []string{"admin_docs.test_utils.T.test_x"}); g[0] != "admin_docs.test_utils.T.test_x" {
		t.Fatalf("dotted label mangled: %v", g)
	}
	// pytest paths untouched.
	if g := selectorsForCommand([]string{"pytest"}, "", []string{"tests/test_x.py::TestY::test_z"}); g[0] != "tests/test_x.py::TestY::test_z" {
		t.Fatalf("pytest node-id must pass through, got %v", g)
	}
}

// The HEALED formula path once ran django's whole suite because a single-string
// command ("python runtests.py") and/or a missing cwd mangled the test label.
// tokenizeCommand splits the argv; djangoTestRoot falls back to tests/ for a
// bare runtests.py with no cwd. Both shapes must yield the narrow dotted label.
func TestHealedDjangoFormulaSelectors(t *testing.T) {
	if got := tokenizeCommand([]string{"python runtests.py"}); len(got) != 2 || got[1] != "runtests.py" {
		t.Fatalf("single-string command must tokenize to argv, got %v", got)
	}
	if got := tokenizeCommand([]string{"pytest"}); len(got) != 1 {
		t.Fatalf("atomic command must pass through, got %v", got)
	}
	if got := tokenizeCommand([]string{"cd tests && python runtests.py"}); len(got) != 1 {
		t.Fatalf("shell string must NOT be split, got %v", got)
	}
	sel := []string{"tests/admin_docs/test_utils.py"}
	// tokenized + cwd, single-string + cwd, and bare + NO cwd all → dotted label.
	for _, c := range []struct {
		cmd []string
		cwd string
	}{
		{[]string{"python", "runtests.py"}, "tests"},
		{[]string{"python runtests.py"}, "tests"},
		{[]string{"python", "runtests.py"}, ""}, // no cwd → tests/ fallback
	} {
		got := selectorsForCommand(c.cmd, c.cwd, sel)
		if len(got) != 1 || got[0] != "admin_docs.test_utils" {
			t.Fatalf("cmd=%v cwd=%q → %v, want [admin_docs.test_utils]", c.cmd, c.cwd, got)
		}
	}
}

// PROBE early-stop: a no-selector probe cancels the instant the runner
// materializes, so a would-hang suite returns in well under its budget and is
// marked Materialized (healthy) — the fast-pre-warm mechanism.
func TestRunFormulaStructured_ProbeEarlyStopsOnMaterialization(t *testing.T) {
	requireUv(t)
	// Prints a materialization marker, then sleeps 60s. With early-stop the run
	// returns in ~1s; without it, it would block until the 20s ctx budget.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	run, err := RunFormulaStructured(ctx, t.TempDir(), TestFormulaSpec{
		Command:   []string{"python", "-c", "print('Creating test database for alias default'); import time; time.sleep(60)"},
		Output:    OutputUnittestText,
		NoProject: true,
		// NO selectors → probe mode.
	})
	dur := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !run.Materialized {
		t.Fatalf("probe that launched the runner must be Materialized, got %+v", run)
	}
	if dur > 15*time.Second {
		t.Fatalf("probe took %s — early-stop did not cancel on materialization", dur.Round(time.Second))
	}
}

// Django's production formula always carries --keepdb. On a warm workspace it
// therefore announces "Using existing test database" rather than "Creating
// test database". Both messages prove the runner launched; missing the warm
// form made a health probe execute Django's entire 12k-test suite and report
// unrelated baseline failures to the agent.
func TestRunFormulaStructured_ProbeEarlyStopsWhenDjangoReusesDatabase(t *testing.T) {
	requireUv(t)
	for _, test := range []struct {
		name   string
		marker string
	}{
		{name: "primary", marker: "Using existing test database for alias 'default'..."},
		{name: "parallel clone", marker: "Using existing clone test database for alias 'default'..."},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			start := time.Now()
			run, err := RunFormulaStructured(ctx, t.TempDir(), TestFormulaSpec{
				Command:   []string{"python", "-c", "print(" + strconv.Quote(test.marker) + "); import time; time.sleep(60)"},
				Output:    OutputUnittestText,
				NoProject: true,
			})
			dur := time.Since(start)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !run.Materialized {
				t.Fatalf("warm django probe that launched the runner must be Materialized, got %+v", run)
			}
			if dur > 15*time.Second {
				t.Fatalf("warm django probe took %s — early-stop did not recognize the reused database", dur.Round(time.Second))
			}
		})
	}
}
