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
		ExcludeNewer:     "2022-07-27T14:44:33Z",
		With:             []string{"numpy>=1.19", "cython"},
		Requirements:     []string{"build-requirements.txt"},
		DependencyGroups: []string{"dev"},
		Extras:           []string{"test", "testing"},
		EditableTarget:   "/w",
	}
	dependencies := strings.Join(venvDependencyInstallArgs("/w/.mind-venv/bin/python", spec), " ")
	wantDependencies := "pip install --python /w/.mind-venv/bin/python --exclude-newer 2022-07-27T14:44:33Z pip setuptools -r build-requirements.txt numpy>=1.19 cython --group dev"
	if dependencies != wantDependencies {
		t.Fatalf("dependency install:\n got %q\nwant %q", dependencies, wantDependencies)
	}
	editable := strings.Join(venvEditableInstallArgs("/w/.mind-venv/bin/python", spec), " ")
	wantEditable := "pip install --python /w/.mind-venv/bin/python --exclude-newer 2022-07-27T14:44:33Z --no-build-isolation --extra test --extra testing -e /w"
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

func TestVenvDependencyInstallArgsAlwaysMaterializesHistoricalPackaging(t *testing.T) {
	got := strings.Join(venvDependencyInstallArgs("/w/.mind-venv/bin/python", TestFormulaSpec{}), " ")
	if got != "pip install --python /w/.mind-venv/bin/python pip setuptools" {
		t.Fatalf("minimal dependency install args = %q", got)
	}
}

func TestHistoricalEditableFallbackIsCapabilityBound(t *testing.T) {
	observed := "AttributeError: module 'setuptools.build_meta' has no attribute 'build_editable'"
	if !editableHookUnavailable(observed) {
		t.Fatal("observed pre-PEP-660 setuptools failure must select historical pip")
	}
	for _, unrelated := range []string{
		"build_editable failed: compiler unavailable",
		"ModuleNotFoundError: setuptools",
		"ordinary build failure",
	} {
		if editableHookUnavailable(unrelated) {
			t.Fatalf("unrelated build failure selected fallback: %q", unrelated)
		}
	}

	args := strings.Join(venvHistoricalEditableInstallArgs(TestFormulaSpec{
		NoBuildIsolation: true,
		EditableTarget:   "/w",
		Extras:           []string{"test", "testing"},
	}), " ")
	want := "-m pip install --no-build-isolation -e /w[test,testing]"
	if args != want {
		t.Fatalf("historical editable args = %q, want %q", args, want)
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
	if venvProvisionHash(base) == venvProvisionHash(TestFormulaSpec{Python: "3.9", EditableTarget: "/w", With: []string{"numpy", "cython"}, DependencyGroups: []string{"dev"}}) {
		t.Fatal("a changed dependency-group set must change the hash")
	}
	if venvProvisionHash(base) == venvProvisionHash(TestFormulaSpec{Python: "3.9", EditableTarget: "/w", With: []string{"numpy", "cython"}, Extras: []string{"test"}}) {
		t.Fatal("a changed optional-extra set must change the hash")
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
