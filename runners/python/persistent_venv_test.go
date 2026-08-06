package python

import (
	"strings"
	"testing"
)

// The persistent venv materializes requirements/build dependencies in a
// separate uv invocation before the no-isolation editable build. Putting both
// in one argv allowed uv to prepare project metadata before setuptools/cython
// had actually reached the venv.
func TestVenvInstallArgsMaterializeDependenciesBeforeEditableProject(t *testing.T) {
	spec := TestFormulaSpec{
		NoBuildIsolation: true,
		With:             []string{"setuptools", "numpy>=1.19", "cython"},
		Requirements:     []string{"build-requirements.txt"},
		EditableTarget:   "/w",
	}
	dependencies := strings.Join(venvDependencyInstallArgs("/w/.mind-venv/bin/python", spec), " ")
	wantDependencies := "pip install --python /w/.mind-venv/bin/python -r build-requirements.txt setuptools numpy>=1.19 cython"
	if dependencies != wantDependencies {
		t.Fatalf("dependency install:\n got %q\nwant %q", dependencies, wantDependencies)
	}
	editable := strings.Join(venvEditableInstallArgs("/w/.mind-venv/bin/python", spec), " ")
	wantEditable := "pip install --python /w/.mind-venv/bin/python --no-build-isolation -e /w"
	if editable != wantEditable {
		t.Fatalf("editable install:\n got %q\nwant %q", editable, wantEditable)
	}
	if strings.Contains(dependencies, "--no-build-isolation") || strings.Contains(dependencies, " -e ") {
		t.Fatalf("dependency install must complete before editable build: %q", dependencies)
	}
	if strings.Contains(editable, "setuptools") || strings.Contains(editable, "build-requirements") {
		t.Fatalf("editable invocation must not combine peer dependencies: %q", editable)
	}
}

func TestVenvDependencyInstallArgsSkipsEmptyProvisioning(t *testing.T) {
	if got := venvDependencyInstallArgs("/w/.mind-venv/bin/python", TestFormulaSpec{}); got != nil {
		t.Fatalf("empty dependency install args = %v, want nil", got)
	}
}

// A changed dep set changes the provision hash (forces rebuild); an unchanged
// one is stable (reuses the warm venv).
func TestVenvProvisionHashStableAndSensitive(t *testing.T) {
	base := TestFormulaSpec{Python: "3.9", EditableTarget: "/w", With: []string{"numpy", "cython"}}
	if venvProvisionHash(base) != venvProvisionHash(TestFormulaSpec{Python: "3.9", EditableTarget: "/w", With: []string{"cython", "numpy"}}) {
		t.Fatal("hash must be order-independent for the same dep set")
	}
	if venvProvisionHash(base) == venvProvisionHash(TestFormulaSpec{Python: "3.10", EditableTarget: "/w", With: []string{"numpy", "cython"}}) {
		t.Fatal("a changed python pin must change the hash")
	}
}

// PersistentVenv auto-enables for the C-extension (no_build_isolation) case and
// stays OFF for pure-Python django. BuildUvArgs then skips --with-editable.
func TestPersistentVenvTriggerAndArgs(t *testing.T) {
	cext := SpecFromFormula([]string{"pytest"}, OutputJUnitXML, nil,
		map[string]string{"editable": "true", "no_build_isolation": "true", "with": "numpy,cython"}, nil)
	if !cext.PersistentVenv {
		t.Fatal("C-extension project (no_build_isolation) must enable PersistentVenv")
	}
	django := SpecFromFormula([]string{"python", "runtests.py"}, OutputUnittestText, nil,
		map[string]string{"editable": "true", "cwd": "tests"}, nil)
	if django.PersistentVenv {
		t.Fatal("pure-Python django must NOT enable PersistentVenv")
	}
	// With a provisioned venv, BuildUvArgs runs against it and drops --with-editable.
	cext.venvPython = "/w/.mind-venv/bin/python"
	got := strings.Join(BuildUvArgs(cext, "/tmp/j.xml"), " ")
	if strings.Contains(got, "--with-editable") || !strings.Contains(got, "--python /w/.mind-venv/bin/python") {
		t.Fatalf("venv run must use the venv python and skip --with-editable, got %q", got)
	}
}
