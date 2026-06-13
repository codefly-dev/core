package python

import (
	"strings"
	"testing"
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
		NoProject:    true,
		Python:       "3.9",
		Editable:     true,
		Requirements: []string{"requirements/tests.txt"},
		With:         []string{"tox<4", "setuptools"},
		Command:      []string{"pytest"},
		Selectors:    []string{"tests/test_x.py::test_y"},
		Output:       OutputJUnitXML,
	}, "/tmp/j.xml"), " ")
	want := "run --no-project --python 3.9 --with-editable . " +
		"--with-requirements requirements/tests.txt --with tox<4 --with setuptools " +
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
			"no_project":   "true",
			"python":       "3.9",
			"editable":     "true",
			"with":         "tox<4, setuptools",
			"requirements": "requirements/tests.txt",
		},
		[]string{"tests/test_x.py::test_y"},
	)
	got := strings.Join(BuildUvArgs(spec, "/tmp/j.xml"), " ")
	want := "run --no-project --python 3.9 --with-editable . " +
		"--with-requirements requirements/tests.txt --with tox<4 --with setuptools " +
		"pytest --junitxml=/tmp/j.xml tests/test_x.py::test_y"
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
	if len(spec.Env) != 1 || spec.Env[0].Key != "PYTHONDONTWRITEBYTECODE" {
		t.Fatalf("env not carried: %+v", spec.Env)
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
