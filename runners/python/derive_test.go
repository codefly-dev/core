package python

import (
	"os"
	"os/exec"
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
	// requires-python ">=3.9" is a LOWER BOUND, not an interpreter choice — the
	// floor is never pinned (it's often an EOL version uv can't install). This is
	// a non-git temp dir, so commit-date inference can't run and the agent falls
	// back to its default managed interpreter.
	if prov["python"] != defaultManagedPython {
		t.Fatalf("python = %q, want %q (>= floor not pinned; no git → agent default)", prov["python"], defaultManagedPython)
	}
	if prov["editable"] != "true" || prov["no_project"] != "true" {
		t.Fatalf("expected editable + no_project provisioning, got %v", prov)
	}
	if !strings.Contains(prov["requirements"], "requirements/tests.txt") {
		t.Fatalf("requirements = %q, want requirements/tests.txt", prov["requirements"])
	}
}

// TestDeriveRequirementFiles_SkipsMinVersionMatrix locks the second flask block:
// a "-min" minimum-version pin matrix (click==8.0.0) must NOT be installed
// alongside the canonical test deps (click==8.1.3) — that made uv resolution
// unsatisfiable and blocked every test.
func TestDeriveRequirementFiles_SkipsMinVersionMatrix(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "requirements"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "requirements/tests.txt", "click==8.1.3\npytest\n")
	writeFile(t, dir, "requirements/tests-pallets-min.txt", "click==8.0.0\n")
	writeFile(t, dir, "requirements/dev.txt", "click==8.1.3\n")
	got := deriveRequirementFiles(dir)
	has := func(s string) bool {
		for _, g := range got {
			if strings.Contains(g, s) {
				return true
			}
		}
		return false
	}
	if !has("tests.txt") {
		t.Errorf("canonical tests.txt missing; got %v", got)
	}
	if has("-min") {
		t.Errorf("minimum-version matrix must be skipped; got %v", got)
	}
}

// TestInferPythonFromCommitDate locks the "don't go forward in time" rule: the
// interpreter is the newest CPython GA'd on or before the repo's HEAD commit
// date, so a 2022-era repo (flask-5014) never runs on a 2023+ Python that breaks
// its test stack.
func TestInferPythonFromCommitDate(t *testing.T) {
	cases := []struct{ commit, want string }{
		{"2022-04-01T00:00:00Z", "3.10"}, // flask-5014 era — 3.11 GA'd Oct 2022
		{"2023-06-01T00:00:00Z", "3.11"},
		{"2024-01-01T00:00:00Z", "3.12"},
		{"2020-01-01T00:00:00Z", "3.8"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		gitCommitAt(t, dir, c.commit)
		if v := inferPythonFromCommitDate(dir); v != c.want {
			t.Errorf("commit %s: inferred %q, want %q", c.commit, v, c.want)
		}
	}
	if v := inferPythonFromCommitDate(t.TempDir()); v != "" {
		t.Errorf("non-git dir: inferred %q, want empty (caller falls back to default)", v)
	}
}

func gitCommitAt(t *testing.T, dir, iso string) {
	t.Helper()
	run := func(env []string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(nil, "init", "-q")
	run(nil, "config", "user.email", "t@t")
	run(nil, "config", "user.name", "t")
	writeFile(t, dir, "f.txt", "x")
	run(nil, "add", ".")
	run([]string{"GIT_AUTHOR_DATE=" + iso, "GIT_COMMITTER_DATE=" + iso}, "commit", "-qm", "c")
}

// TestDerivePythonVersion_PinPolicy locks the fix for the SWE-bench flask block:
// a `>=` lower bound must NOT be pinned (the floor is often an EOL version uv
// cannot install → "No interpreter found", 0 tests run), while an explicit
// choice (.python-version, ==, ~=, bare version) IS honored.
func TestDerivePythonVersion_PinPolicy(t *testing.T) {
	// A lower bound is NOT an interpreter choice; the agent pins its default
	// managed interpreter (not the EOL floor, not uv's newest).
	for _, c := range []string{">=3.7", ">=3.9", ">3.6", ">=3.8,<4"} {
		dir := t.TempDir()
		writeFile(t, dir, "pyproject.toml", "[project]\nrequires-python = \""+c+"\"\n")
		if v := derivePythonVersion(dir); v != defaultManagedPython {
			t.Errorf("requires-python %q: pinned %q, want default %q (floor is a minimum, not a choice)", c, v, defaultManagedPython)
		}
	}
	// No requires-python at all → still the agent's default interpreter.
	if dir := t.TempDir(); derivePythonVersion(dir) != defaultManagedPython {
		t.Errorf("no requires-python: want default %q", defaultManagedPython)
	}
	for c, want := range map[string]string{"==3.11": "3.11", "~=3.9": "3.9", "3.10": "3.10"} {
		dir := t.TempDir()
		writeFile(t, dir, "pyproject.toml", "[project]\nrequires-python = \""+c+"\"\n")
		if v := derivePythonVersion(dir); v != want {
			t.Errorf("requires-python %q: pinned %q, want %q (explicit pin honored)", c, v, want)
		}
	}
	// .python-version is an explicit operator-chosen interpreter — always honored,
	// even when pyproject only states a >= floor.
	dir := t.TempDir()
	writeFile(t, dir, ".python-version", "3.11.4\n")
	writeFile(t, dir, "pyproject.toml", "[project]\nrequires-python = \">=3.7\"\n")
	if v := derivePythonVersion(dir); v != "3.11" {
		t.Errorf(".python-version honored: got %q, want 3.11", v)
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

// TestDeriveBuildSystemRequires locks the source-build provisioning rule:
// pyproject [build-system].requires carrying non-default build deps (numpy,
// cython, …) flows into --with specs + --no-build-isolation; a default
// setuptools/wheel-only list derives nothing.
func TestDeriveBuildSystemRequires(t *testing.T) {
	dir := t.TempDir()
	py := `[build-system]
requires = ["setuptools>=40", "wheel", "numpy>=1.14", "Cython>=0.28"]
build-backend = "setuptools.build_meta"
`
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(py), 0o644); err != nil {
		t.Fatal(err)
	}
	prov := deriveProvisioning(dir)
	if prov["no_build_isolation"] != "true" {
		t.Fatalf("no_build_isolation = %q, want true", prov["no_build_isolation"])
	}
	want := "setuptools>=40,wheel,numpy>=1.14,Cython>=0.28"
	if prov["with"] != want {
		t.Fatalf("with = %q, want %q", prov["with"], want)
	}

	defaultsOnly := t.TempDir()
	py2 := "[build-system]\nrequires = [\"setuptools\", \"wheel\"]\n"
	if err := os.WriteFile(filepath.Join(defaultsOnly, "pyproject.toml"), []byte(py2), 0o644); err != nil {
		t.Fatal(err)
	}
	prov2 := deriveProvisioning(defaultsOnly)
	if prov2["with"] != "" || prov2["no_build_isolation"] != "" {
		t.Fatalf("defaults-only build-system must derive nothing, got with=%q nbi=%q", prov2["with"], prov2["no_build_isolation"])
	}
}

// django's runtests.py recreates test DBs on every run (minutes each);
// --keepdb is auto-injected so the agent's repeated reproduce→edit→verify
// runs and the grader reuse the DB. No-op for pytest; idempotent.
func TestWithDjangoKeepDB(t *testing.T) {
	got := withDjangoKeepDB([]string{"python", "runtests.py"})
	if len(got) != 3 || got[2] != "--keepdb" {
		t.Fatalf("runtests.py must gain --keepdb, got %v", got)
	}
	if again := withDjangoKeepDB(got); len(again) != 3 {
		t.Fatalf("--keepdb must be idempotent, got %v", again)
	}
	if py := withDjangoKeepDB([]string{"pytest", "-q"}); len(py) != 2 {
		t.Fatalf("non-django command must be unchanged, got %v", py)
	}
}
