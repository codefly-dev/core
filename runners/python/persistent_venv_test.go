package python

import (
	"strings"
	"testing"
)

// The persistent-venv install argv puts deps + requirements BEFORE the editable
// project (so the no-isolation build sees numpy/cython), targets the venv's
// python, and installs the project editable. Pure arg construction — no uv exec.
func TestVenvInstallArgs(t *testing.T) {
	got := strings.Join(venvInstallArgs("/w/.mind-venv/bin/python", TestFormulaSpec{
		NoBuildIsolation: true,
		With:             []string{"numpy>=1.19", "cython"},
		Requirements:     []string{"build-requirements.txt"},
		EditableTarget:   "/w",
	}), " ")
	want := "pip install --python /w/.mind-venv/bin/python --no-build-isolation -r build-requirements.txt numpy>=1.19 cython -e /w"
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
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
