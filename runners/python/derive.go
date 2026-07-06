package python

// derive.go — the python plugin DERIVES how to test a project from the project
// itself: the COMMAND from its own declarations (tox / Makefile / CI / README —
// never framework names) and the PROVISIONING (editable install, python version,
// requirement files, test extras) from its packaging metadata. This is what lets
// a formula-less Runtime.Test "just run the project's tests": Mind sends no
// command, the plugin figures it out + installs deps. Ported from Mind's
// framework-blind pkg/testprofile (command extraction) + new provisioning
// derivation that belongs here, where uv/python knowledge lives.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// selectorsToken marks where specific tests get injected in a derived command.
// codefly appends req.Filters as selectors, so DeriveFormula strips this token
// from the argv it returns.
const selectorsToken = "{selectors}"

// DeriveFormula inspects sourceDir and returns a runnable test formula: the argv
// (command), the structured-output format, env, and uv provisioning — all derived
// from the project. ok=false means the project declares nothing runnable (caller
// falls back to its default runner). No framework is hardcoded: the command comes
// from the project's text; provisioning comes from its packaging metadata.
func DeriveFormula(sourceDir string) (cmd []string, output string, env, prov map[string]string, ok bool) {
	decls := collectDeclarations(sourceDir)
	rawCmd, found := extractCommand(decls)
	if !found {
		return nil, "", nil, nil, false
	}
	output = outputFormatFromCommand(rawCmd)
	argv := commandArgv(rawCmd) // strip {selectors}; codefly adds Filters
	if len(argv) == 0 {
		return nil, "", nil, nil, false
	}
	prov = deriveProvisioning(sourceDir)
	if cwd := djangoRuntestsCwd(sourceDir, argv); cwd != "" {
		prov["cwd"] = cwd
	}
	return argv, output, nil, prov, true
}

// djangoRuntestsCwd resolves the directory a BARE `runtests.py` command must
// run from. django's test runner usually lives in tests/runtests.py; a derived
// command of bare "runtests.py" (no dir) launched from the repo root fails with
// "can't open file 'runtests.py'". Setting cwd=tests fixes it WITHOUT waiting
// for the heal loop to discover it (the real reason django's first test probe
// blocked and healing then thrashed). Empty when the command already carries a
// path (tests/runtests.py runs fine from root) or runtests.py is at the root.
func djangoRuntestsCwd(sourceDir string, argv []string) string {
	bare := false
	for _, a := range argv {
		if strings.Contains(a, "/runtests.py") {
			return "" // command already names the dir; run from root
		}
		if a == "runtests.py" {
			bare = true
		}
	}
	if !bare {
		return ""
	}
	if fileExists(filepath.Join(sourceDir, "runtests.py")) {
		return "" // already at the root
	}
	if fileExists(filepath.Join(sourceDir, "tests", "runtests.py")) {
		return "tests"
	}
	return ""
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// withDjangoKeepDB appends --keepdb to a django `runtests.py` command.
// django's test runner RECREATES the test databases (running every migration
// for the whole test project) on EVERY invocation — 5-9 minutes each,
// dominating the run regardless of how narrow the selector is. --keepdb reuses
// the databases across invocations, so only the FIRST run pays DB creation and
// every subsequent run (the agent's reproduce→edit→verify loop, and the
// post-hoc grader) is seconds. A working agent that needs 3-4 test runs was
// timing out purely on repeated DB setup. django recreates the DB itself if a
// migration actually changed, so --keepdb stays correct across model edits.
// No-op for non-django commands (pytest, unittest discover) and idempotent.
func withDjangoKeepDB(argv []string) []string {
	isRuntests := false
	for _, a := range argv {
		if strings.Contains(a, "runtests.py") {
			isRuntests = true
		}
		if a == "--keepdb" {
			return argv // already present
		}
	}
	if !isRuntests {
		return argv
	}
	return append(argv, "--keepdb")
}

// DeriveProvisioning exposes the packaging-metadata provisioning derivation
// (editable install, python pin, requirement files) for callers that already
// HAVE a command — a SUPPLIED formula names WHAT to run, but the uv
// environment around it is still the plugin's to derive. Without this, a
// caller-supplied bare command (e.g. django's captured
// "cd tests && python runtests.py") runs `uv run` with no --with-editable,
// and the project's own package isn't importable ("Django module not found").
func DeriveProvisioning(sourceDir string) map[string]string {
	return deriveProvisioning(sourceDir)
}

// EnrichSuppliedProvisioning fills the gaps in a SUPPLIED formula's
// provisioning bag from the project's own packaging metadata. The caller
// (Mind, service.yaml, a healed runtime config) owns WHAT to run; the uv
// environment around it — editable install of the project, interpreter pin,
// requirement files, build deps — is the plugin's to derive. Explicitly
// supplied keys always win, so a caller can still force editable=false or a
// python version. This is THE shared enrichment point: the gRPC agent's
// resolveTestFormula AND Mind's in-process runtime both call it, so the
// health PROBE and the Test RPC resolve identical formulas. Observed failure
// this closes: a captured django formula ("cd tests && python runtests.py")
// arriving with an empty bag ran `uv run` without --with-editable . and
// env-blocked with "ModuleNotFoundError: No module named 'django'".
func EnrichSuppliedProvisioning(supplied map[string]string, sourceDir string) map[string]string {
	if sourceDir == "" {
		return supplied
	}
	derived := deriveProvisioning(sourceDir)
	if len(derived) == 0 {
		return supplied
	}
	merged := make(map[string]string, len(derived)+len(supplied))
	for k, v := range derived {
		merged[k] = v
	}
	for k, v := range supplied {
		merged[k] = v
	}
	return merged
}

// ── declaration collection (os-backed; the plugin sees project files) ──

type declaration struct {
	source string
	path   string
	text   string
}

const (
	srcCI       = "ci-workflow"
	srcTox      = "tox"
	srcMakefile = "makefile"
	srcNox      = "nox"
	srcReadme   = "readme"
)

var declarationCandidates = []struct{ path, source string }{
	{"tox.ini", srcTox},
	{"Makefile", srcMakefile},
	{"GNUmakefile", srcMakefile},
	{"noxfile.py", srcNox},
	{"CONTRIBUTING.md", srcReadme},
	{"CONTRIBUTING.rst", srcReadme},
	{"README.md", srcReadme},
	{"README.rst", srcReadme},
}

func collectDeclarations(dir string) []declaration {
	var decls []declaration
	// CI workflows first (highest signal, dynamic names).
	wfDir := filepath.Join(dir, ".github", "workflows")
	if entries, err := os.ReadDir(wfDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			switch strings.ToLower(filepath.Ext(e.Name())) {
			case ".yml", ".yaml":
				if text := readFileString(filepath.Join(wfDir, e.Name())); strings.TrimSpace(text) != "" {
					decls = append(decls, declaration{source: srcCI, path: e.Name(), text: text})
				}
			}
		}
	}
	for _, c := range declarationCandidates {
		if text := readFileString(filepath.Join(dir, c.path)); strings.TrimSpace(text) != "" {
			decls = append(decls, declaration{source: c.source, path: c.path, text: text})
		}
	}
	return decls
}

func readFileString(p string) string {
	b, err := os.ReadFile(p) //nolint:gosec // project file under the workspace dir
	if err != nil {
		return ""
	}
	return string(b)
}

// ── command extraction (ported verbatim-in-spirit from pkg/testprofile) ──

func extractCommand(decls []declaration) (string, bool) {
	type extractor func(declaration) (string, bool)
	order := []struct {
		src string
		fn  extractor
	}{
		{srcCI, extractFromCI},
		{srcTox, extractFromTox},
		{srcMakefile, extractFromMakefile},
		{srcNox, extractFromMakefile}, // noxfile sessions read like recipes
		{srcReadme, extractFromReadme},
	}
	for _, o := range order {
		for _, d := range decls {
			if d.source != o.src {
				continue
			}
			if cmd, ok := o.fn(d); ok {
				return normalizeCommand(cmd), true
			}
		}
	}
	return "", false
}

func normalizeCommand(cmd string) string {
	cmd = resolveConfigTokens(cmd)
	cmd = normalizeArgToken(cmd)
	cmd = dropUnresolvedArgs(cmd)
	return strings.TrimSpace(cmd)
}

// commandArgv splits a normalized command into argv, dropping the {selectors}
// token (codefly injects specific tests via req.Filters).
func commandArgv(cmd string) []string {
	var argv []string
	for _, f := range strings.Fields(cmd) {
		if f == selectorsToken {
			continue
		}
		argv = append(argv, f)
	}
	return argv
}

var reToxPosargs = regexp.MustCompile(`\{posargs(?::[^}]*)?\}`)
var reMakeArgs = regexp.MustCompile(`\$\((?:ARGS|TESTS|PYTEST_ARGS|TEST_ARGS)\)`)

func normalizeArgToken(cmd string) string {
	cmd = reToxPosargs.ReplaceAllString(cmd, selectorsToken)
	cmd = reMakeArgs.ReplaceAllString(cmd, selectorsToken)
	return strings.TrimSpace(cmd)
}

func resolveConfigTokens(cmd string) string {
	cmd = strings.ReplaceAll(cmd, "{envpython}", "python")
	cmd = strings.ReplaceAll(cmd, "{toxinidir}/", "")
	cmd = strings.ReplaceAll(cmd, "{toxinidir}", ".")
	return cmd
}

func dropUnresolvedArgs(cmd string) string {
	fields := strings.Fields(cmd)
	keep := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == selectorsToken {
			keep = append(keep, f)
			continue
		}
		if strings.Contains(f, "{") && strings.Contains(f, "}") {
			continue
		}
		keep = append(keep, f)
	}
	return strings.Join(keep, " ")
}

func outputFormatFromCommand(cmd string) string {
	low := strings.ToLower(cmd)
	if strings.Contains(low, "junitxml") || strings.Contains(low, "junit-xml") || strings.Contains(low, "--junit") {
		return OutputJUnitXML
	}
	// The python PLUGIN is allowed to know its runners' output shapes: pytest
	// emits JUnit (the runner adds --junitxml for OutputJUnitXML); django's
	// runtests / unittest emit the text format. This is runner knowledge living
	// where it belongs — not framework knowledge leaking into Mind.
	if strings.Contains(low, "pytest") {
		return OutputJUnitXML
	}
	return "unittest-text"
}

func extractFromTox(d declaration) (string, bool) {
	lines := strings.Split(d.text, "\n")
	inTestEnv, collecting := false, false
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inTestEnv = isDefaultTestEnv(trimmed)
			collecting = false
			continue
		}
		if !inTestEnv {
			continue
		}
		if collecting {
			if line != trimmed && trimmed != "" {
				if cmd := firstCommandLine(trimmed); cmd != "" {
					return cmd, true
				}
			} else if trimmed == "" {
				continue
			} else {
				collecting = false
			}
		}
		if k, v, ok := iniKey(trimmed); ok && k == "commands" {
			if v != "" {
				if cmd := firstCommandLine(v); cmd != "" {
					return cmd, true
				}
			}
			collecting = true
		}
	}
	return "", false
}

func extractFromMakefile(d declaration) (string, bool) {
	lines := strings.Split(d.text, "\n")
	inTarget := false
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if reMakeTestTarget.MatchString(line) {
			inTarget = true
			continue
		}
		if inTarget {
			if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ") {
				recipe := strings.TrimSpace(strings.TrimLeft(line, "\t "))
				recipe = strings.TrimLeft(recipe, "@-+")
				if cmd := firstCommandLine(recipe); cmd != "" {
					return cmd, true
				}
			} else if strings.TrimSpace(line) != "" {
				inTarget = false
			}
		}
	}
	return "", false
}

var reMakeTestTarget = regexp.MustCompile(`^(test|tests|check|pytest)[\w-]*:`)

func extractFromCI(d declaration) (string, bool) {
	lines := strings.Split(d.text, "\n")
	recentName := ""
	for i := range lines {
		trimmed := strings.TrimSpace(lines[i])
		if v, ok := yamlValue(trimmed, "name"); ok {
			recentName = strings.ToLower(v)
		}
		if v, ok := yamlValue(trimmed, "run"); ok {
			if strings.Contains(recentName, "test") {
				cmd := v
				if cmd == "|" || cmd == ">" || cmd == "" {
					cmd = nextBlockLine(lines, i)
				}
				if cmd = firstCommandLine(cmd); cmd != "" {
					return cmd, true
				}
			}
		}
	}
	return "", false
}

func extractFromReadme(d declaration) (string, bool) {
	lines := strings.Split(d.text, "\n")
	near := false
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		low := strings.ToLower(trimmed)
		if strings.Contains(low, "test") &&
			(strings.HasPrefix(trimmed, "#") || strings.HasSuffix(trimmed, ":") ||
				strings.Contains(low, "run the test") || isHeadingLike(trimmed)) {
			near = true
			continue
		}
		if !near {
			continue
		}
		cand := strings.TrimSpace(strings.TrimPrefix(trimmed, "$"))
		if cand == "" || strings.HasPrefix(cand, "```") {
			continue
		}
		if looksLikeCommand(cand) {
			return cand, true
		}
	}
	return "", false
}

func firstCommandLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || !looksLikeCommand(s) {
		return ""
	}
	return s
}

func looksLikeCommand(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	first := strings.Fields(s)[0]
	if strings.ContainsAny(first, "#<>|") {
		return false
	}
	return reCommandHead.MatchString(first)
}

var reCommandHead = regexp.MustCompile(`^[A-Za-z_./{][\w./{}$-]*$`)

func isDefaultTestEnv(header string) bool {
	h := strings.TrimSpace(header)
	return h == "[testenv]" || strings.HasPrefix(h, "[testenv:py")
}

func isHeadingLike(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "$") || strings.ContainsAny(line, "`$|<>") {
		return false
	}
	return len(strings.Fields(line)) <= 4
}

func iniKey(line string) (key, val string, ok bool) {
	if i := strings.IndexAny(line, "=:"); i > 0 {
		return strings.TrimSpace(strings.ToLower(line[:i])), strings.TrimSpace(line[i+1:]), true
	}
	return "", "", false
}

func yamlValue(line, key string) (string, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
	if rest, ok := strings.CutPrefix(line, key+":"); ok {
		return strings.TrimSpace(rest), true
	}
	return "", false
}

func nextBlockLine(lines []string, from int) string {
	for j := from + 1; j < len(lines); j++ {
		if t := strings.TrimSpace(lines[j]); t != "" {
			return t
		}
	}
	return ""
}

// ── provisioning derivation (NEW; uv/python knowledge belongs here) ──

// deriveProvisioning reads the project's packaging metadata and produces the uv
// provisioning map SpecFromFormula consumes: no_project + editable install,
// python version (pyproject requires-python / .python-version), requirement
// files (requirements*.txt, requirements/*.txt), and test extras (pyproject
// optional-dependencies test/tests/dev). Best-effort: every field is optional and
// the tooling inner loop heals what derivation can't see.
func deriveProvisioning(dir string) map[string]string {
	prov := map[string]string{"no_project": "true"}
	// --with-editable . only makes sense when the project IS an installable
	// package (setup.py / setup.cfg / pyproject.toml). A bare test directory
	// with no packaging metadata must not get an editable install injected —
	// uv would fail the build instead of running the tests.
	if hasInstallablePackaging(dir) {
		prov["editable"] = "true"
	}
	if v := derivePythonVersion(dir); v != "" {
		prov["python"] = v
	}
	if reqs := deriveRequirementFiles(dir); len(reqs) > 0 {
		prov["requirements"] = strings.Join(reqs, ",")
	}
	// Source builds of C-extension projects: pyproject [build-system].requires
	// names the packages the BUILD needs (numpy, cython, setuptools plugins…).
	// Editable installs run that build, so carry the declared build deps as
	// --with specs and disable build isolation so the build sees them. Pure
	// project data — no package names are hardcoded here.
	if buildReqs := deriveBuildSystemRequires(dir); len(buildReqs) > 0 {
		prov["with"] = strings.Join(buildReqs, ",")
		prov["no_build_isolation"] = "true"
	}
	// NOTE: pyproject [project.optional-dependencies] test/dev extras (`.[test]`)
	// are a known gap — SpecFromFormula has no `--extra` flag yet. When a project
	// needs them, the tooling inner loop heals provisioning until the env runs;
	// add `extras` support to SpecFromFormula to derive them up front.
	return prov
}

// hasInstallablePackaging reports whether the project declares packaging
// metadata an editable install can build from: setup.py, setup.cfg, or a
// pyproject.toml. (django's setup.cfg-declared package is the canonical case:
// its tests import the package, so the derived/enriched provisioning must
// install it editable for ANY supplied or derived test command to run.)
func hasInstallablePackaging(dir string) bool {
	for _, name := range []string{"setup.py", "setup.cfg", "pyproject.toml"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// deriveBuildSystemRequires parses pyproject [build-system] requires entries.
// Only non-default build deps matter: setuptools/wheel are what uv's default
// isolated build already provides, so a requires list of just those returns
// nil (no reason to disable isolation).
func deriveBuildSystemRequires(dir string) []string {
	py := readFileString(filepath.Join(dir, "pyproject.toml"))
	if py == "" {
		return nil
	}
	m := reBuildRequires.FindStringSubmatch(py)
	if len(m) != 2 {
		return nil
	}
	var reqs []string
	nonDefault := false
	for _, entry := range reBuildRequireEntry.FindAllStringSubmatch(m[1], -1) {
		spec := strings.TrimSpace(entry[1])
		if spec == "" {
			continue
		}
		name := strings.ToLower(spec)
		for i, r := range name {
			if !(r == '-' || r == '_' || r == '.' || ('a' <= r && r <= 'z') || ('0' <= r && r <= '9')) {
				name = name[:i]
				break
			}
		}
		if name != "setuptools" && name != "wheel" {
			nonDefault = true
		}
		reqs = append(reqs, spec)
	}
	if !nonDefault {
		return nil
	}
	return reqs
}

var reBuildRequires = regexp.MustCompile(`(?s)\[build-system\][^\[]*?requires\s*=\s*\[(.*?)\]`)
var reBuildRequireEntry = regexp.MustCompile(`["']([^"']+)["']`)

var rePyRequires = regexp.MustCompile(`requires-python\s*=\s*["']([^"']+)["']`)
var rePyVerNum = regexp.MustCompile(`3\.\d+`)

func derivePythonVersion(dir string) string {
	if v := strings.TrimSpace(readFileString(filepath.Join(dir, ".python-version"))); v != "" {
		if m := rePyVerNum.FindString(v); m != "" {
			return m
		}
	}
	if py := readFileString(filepath.Join(dir, "pyproject.toml")); py != "" {
		if m := rePyRequires.FindStringSubmatch(py); len(m) == 2 {
			if v := pinFromRequiresPython(m[1]); v != "" {
				return v
			}
		}
	}
	// No explicit interpreter choice (no .python-version, and requires-python is
	// only a lower bound or absent). DON'T leave it to uv (which picks the NEWEST
	// installed Python) and DON'T pin the requires-python FLOOR (often EOL /
	// uninstallable). Instead infer from the repo's HEAD commit date: the test
	// stack was written against interpreters that EXISTED then, so a newer Python
	// often breaks it (3.12 removed ast.Str, crashing 2022-era conftests like
	// flask's). Pick the newest interpreter GA'd on or before that date — "don't
	// go forward in time." Falls back to a stable default when there's no git.
	if v := inferPythonFromCommitDate(dir); v != "" {
		return v
	}
	return defaultManagedPython
}

// defaultManagedPython is the interpreter the python agent pins when the project
// selects none AND the commit date can't be read (no git). 3.11 is the newest
// interpreter that still runs the older test stacks common in the SWE-bench
// corpus (3.12 removed ast.Str, breaking their conftests); uv can always
// download it.
const defaultManagedPython = "3.11"

// pythonReleases maps each CPython minor to its GA (final) release date, NEWEST
// FIRST. The python agent owns this (interpreter knowledge is its domain) and
// uses it to avoid running a project on a Python that did not exist when the
// project was last committed.
var pythonReleases = []struct {
	version string
	ga      time.Time
}{
	{"3.14", releaseDate(2025, 10, 7)},
	{"3.13", releaseDate(2024, 10, 7)},
	{"3.12", releaseDate(2023, 10, 2)},
	{"3.11", releaseDate(2022, 10, 24)},
	{"3.10", releaseDate(2021, 10, 4)},
	{"3.9", releaseDate(2020, 10, 5)},
	{"3.8", releaseDate(2019, 10, 14)},
	{"3.7", releaseDate(2018, 6, 27)},
}

func releaseDate(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

// inferPythonFromCommitDate returns the newest CPython minor that had GA'd on or
// before the repo's HEAD commit date, or "" when the date can't be read (not a
// git repo / git unavailable). This is the "don't go forward in time" rule: a
// repo committed in 2022 should run on a 2022-or-earlier interpreter, not 3.13.
func inferPythonFromCommitDate(dir string) string {
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%cI", "HEAD").Output()
	if err != nil {
		return ""
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
	if err != nil {
		return ""
	}
	for _, r := range pythonReleases { // newest first
		if !t.Before(r.ga) {
			return r.version
		}
	}
	return ""
}

// pinFromRequiresPython turns a requires-python constraint into an interpreter
// to pin with `uv run --python` — but ONLY when the project actually pins one.
//
// A LOWER-BOUND constraint (">=3.7", ">3.6") states the MINIMUM supported
// version, NOT the interpreter to run. Pinning that floor is wrong and usually
// FATAL: the floor is often an EOL version uv cannot install at all. ">=3.7"
// made `uv run --python 3.7` fail "No interpreter found for Python 3.7 … uv
// embeds available Python downloads and may require an update" — which blocked
// EVERY test (0 collected) even though uv would happily pick 3.12 unpinned. So
// for a lower bound we return "" and let uv resolve a compatible AVAILABLE
// interpreter. We pin only an exact / compatible-release spec ("==3.11",
// "~=3.9") or a bare version ("3.11") — cases where the project genuinely
// selects an interpreter.
func pinFromRequiresPython(constraint string) string {
	c := strings.TrimSpace(constraint)
	// Lower-bound only ("<" upper bounds aside) → don't pin; uv chooses a
	// compatible interpreter that actually exists on the machine.
	if strings.HasPrefix(c, ">") {
		return ""
	}
	if v := rePyVerNum.FindString(c); v != "" {
		return v
	}
	return ""
}

func deriveRequirementFiles(dir string) []string {
	var out []string
	// Top-level requirements*.txt.
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			n := e.Name()
			if !e.IsDir() && strings.HasPrefix(n, "requirements") && strings.HasSuffix(n, ".txt") {
				out = append(out, n)
			}
		}
	}
	// requirements/ directory (common test-deps layout, e.g. requirements/tests.txt).
	reqDir := filepath.Join(dir, "requirements")
	if entries, err := os.ReadDir(reqDir); err == nil {
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || !strings.HasSuffix(n, ".txt") {
				continue
			}
			if !strings.Contains(n, "test") && !strings.Contains(n, "dev") {
				continue
			}
			// Skip MINIMUM-VERSION pin matrices (e.g. flask's
			// "tests-pallets-min.txt" pinning click==8.0.0). These are a CI job's
			// floor-version CONSTRAINT set, NOT the deps to install — installing
			// them alongside the canonical test deps (and the editable package,
			// which resolves click==8.1.3) makes the env unsatisfiable
			// ("click==8.1.3 and click==8.0.0 … unsatisfiable") and blocks every
			// test. The "-min" infix matches "-min.txt"/"-minimum" without
			// catching legitimate names like "admin".
			if strings.Contains(n, "-min") {
				continue
			}
			out = append(out, filepath.Join("requirements", n))
		}
	}
	return out
}
