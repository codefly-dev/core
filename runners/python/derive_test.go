package python

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DeriveFormula is how a formula-less Test "just runs the project's tests": the
// plugin derives the command from the project's own declarations and the
// provisioning from its packaging metadata. These lock that behavior on real
// fixture projects (temp dirs) — no framework names hardcoded.

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeriveFormula_ToxPytestWithProvisioning(t *testing.T) {
	dir := t.TempDir()
	// The project DECLARES its test command in tox.ini; the plugin reads it
	// verbatim (no framework assumption) and maps {posargs} -> selectors (stripped;
	// codefly appends Filters).
	writeFile(t, dir, "tox.ini", "[testenv]\ndeps = pytest\ncommands = pytest {posargs}\n")
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"x\"\nrequires-python = \">=3.9\"\n")
	writeFile(t, dir, "requirements/tests.txt", "pytest\n")

	cmd, output, _, prov, ok := DeriveFormula(dir)
	if !ok {
		t.Fatal("expected a derived formula")
	}
	if len(cmd) != 1 || cmd[0] != "pytest" {
		t.Fatalf("command = %v, want [pytest]", cmd)
	}
	if output != OutputJUnitXML {
		t.Fatalf("pytest output format = %q, want junit-xml", output)
	}
	if prov["python"] != "3.9" {
		t.Fatalf("python = %q, want 3.9 (from requires-python)", prov["python"])
	}
	if prov["editable"] != "true" || prov["no_project"] != "true" {
		t.Fatalf("expected editable + no_project provisioning, got %v", prov)
	}
	if !strings.Contains(prov["requirements"], "requirements/tests.txt") {
		t.Fatalf("requirements = %q, want requirements/tests.txt", prov["requirements"])
	}
}

func TestDeriveFormula_DjangoRuntestsFromMakefile(t *testing.T) {
	dir := t.TempDir()
	// A Makefile `test:` recipe — django's runner, derived verbatim, no special-casing.
	writeFile(t, dir, "Makefile", "test:\n\tpython tests/runtests.py $(ARGS)\n")
	cmd, output, _, _, ok := DeriveFormula(dir)
	if !ok {
		t.Fatal("expected a derived formula")
	}
	if strings.Join(cmd, " ") != "python tests/runtests.py" {
		t.Fatalf("command = %v, want [python tests/runtests.py]", cmd)
	}
	if output != "unittest-text" {
		t.Fatalf("runtests output = %q, want unittest-text", output)
	}
}

func TestDeriveFormula_CIWorkflowWins(t *testing.T) {
	dir := t.TempDir()
	// CI is the highest-signal source — it declares the EXACT command.
	writeFile(t, dir, ".github/workflows/ci.yml",
		"jobs:\n  t:\n    steps:\n      - name: Run tests\n        run: pytest -q\n")
	// A weaker README mention must NOT win over CI.
	writeFile(t, dir, "README.md", "# Testing\n\n    nosetests\n")
	cmd, _, _, _, ok := DeriveFormula(dir)
	if !ok || cmd[0] != "pytest" {
		t.Fatalf("CI command should win, got %v ok=%v", cmd, ok)
	}
}

func TestDeriveFormula_NothingDeclared(t *testing.T) {
	if _, _, _, _, ok := DeriveFormula(t.TempDir()); ok {
		t.Fatal("a project declaring nothing runnable must return ok=false")
	}
}
