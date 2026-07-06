package python

import (
	"strings"
	"testing"
)

func TestRuntimeEvidenceReportsDetectedEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tox.ini", "[tox]\nenvlist = py\n\n[testenv]\ncommands =\n    python -m pytest {posargs}\n")
	writeFile(t, dir, "pyproject.toml", "[tool.poetry]\nname = \"demo\"\nversion = \"0.1.0\"\n")
	writeFile(t, dir, ".python-version", "3.11\n")
	writeFile(t, dir, "requirements/dev.txt", "pytest\n")

	evidence := RuntimeEvidence(dir)
	for _, want := range []string{
		"language: python",
		"runner_environment_manager: uv",
		"project_environment_manager: poetry",
		"formula_source: derived from project declarations",
		"test_command: python -m pytest",
		"test_output: junit-xml",
		"uv_args: uv run",
		"test command declaration: tox.ini",
		"python project/environment declaration: pyproject.toml",
		"python dependency declaration: requirements/dev.txt",
	} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("runtime evidence missing %q:\n%s", want, evidence)
		}
	}
}

func TestRuntimeEvidenceOnlyReportsExistingSources(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tox.ini", "[testenv]\ncommands = python -m pytest {posargs}\n")
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"demo\"\n")

	evidence := RuntimeEvidence(dir)
	if !strings.Contains(evidence, "project_environment_manager: pyproject") {
		t.Fatalf("runtime evidence should detect pyproject manager:\n%s", evidence)
	}
	if strings.Contains(evidence, "python dependency declaration") {
		t.Fatalf("runtime evidence should not invent dependency source files:\n%s", evidence)
	}
	if strings.Contains(evidence, "requirements.txt") {
		t.Fatalf("runtime evidence should not list absent conventional files:\n%s", evidence)
	}
}
