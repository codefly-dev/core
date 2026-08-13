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
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
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
	var fallback string
	consider := func(value string) (string, bool) {
		cmd := firstCommandLine(stripToxFactor(value))
		if cmd == "" {
			return "", false
		}
		// The passthrough token identifies the command that owns test
		// selection. Setup/diagnostic commands such as `pip freeze` commonly
		// precede it and must not become a successful zero-test formula.
		if reToxPosargs.MatchString(cmd) {
			return cmd, true
		}
		if fallback == "" {
			fallback = cmd
		}
		return "", false
	}
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
				if cmd, selected := consider(trimmed); selected {
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
				if cmd, selected := consider(v); selected {
					return cmd, true
				}
			}
			collecting = true
		}
	}
	return fallback, fallback != ""
}

var reToxFactor = regexp.MustCompile(`^[A-Za-z0-9_!,{}.-]+$`)

// stripToxFactor removes tox's leading environment-factor condition from a
// command. Requiring one factor-grammar token keeps ordinary command colons
// intact while making the selected command runnable outside tox orchestration.
func stripToxFactor(command string) string {
	prefix, rest, found := strings.Cut(strings.TrimSpace(command), ":")
	if found && reToxFactor.MatchString(prefix) && strings.TrimSpace(rest) != "" {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(command)
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
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(d.text), &document); err != nil {
		return "", false
	}
	return ciTestStepCommand(&document)
}

// ciTestStepCommand reads `run` only from mappings that are direct children of
// a workflow `steps` sequence. Step names are optional. Action inputs can expose
// an unrelated nested `with.run`; keeping traversal aware of the `steps`
// boundary prevents that input from becoming the project's test command.
func ciTestStepCommand(node *yaml.Node) (string, bool) {
	return ciTestStepCommandAt(node, false)
}

func ciTestStepCommandAt(node *yaml.Node, workflowStep bool) (string, bool) {
	if node == nil {
		return "", false
	}
	if workflowStep && node.Kind == yaml.MappingNode {
		run := ""
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			if key.Kind != yaml.ScalarNode || value.Kind != yaml.ScalarNode {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(key.Value)) {
			case "run":
				run = value.Value
			}
		}
		if run != "" && !strings.Contains(run, "${{") {
			for _, line := range strings.Split(run, "\n") {
				if command := firstCommandLine(strings.TrimSpace(line)); command != "" && portableCICommand(command) {
					return command, true
				}
			}
		}
	}
	if node.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			if key.Kind == yaml.ScalarNode && strings.EqualFold(strings.TrimSpace(key.Value), "steps") && value.Kind == yaml.SequenceNode {
				for _, step := range value.Content {
					if command, ok := ciTestStepCommandAt(step, true); ok {
						return command, true
					}
				}
				continue
			}
			if command, ok := ciTestStepCommandAt(value, false); ok {
				return command, true
			}
		}
		return "", false
	}
	for _, child := range node.Content {
		if command, ok := ciTestStepCommandAt(child, false); ok {
			return command, true
		}
	}
	return "", false
}

// portableCICommand rejects runner-image absolute executables. A workflow can
// legitimately name the exact interpreter baked into its CI container (for
// example /opt/python/cp38-cp38/bin/python), but that path is not part of the
// project contract and cannot exist in an arbitrary Codefly workspace. Let a
// project-local declaration such as tox.ini supply the portable command.
func portableCICommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	executable := fields[0]
	if strings.HasPrefix(executable, "/") || strings.HasPrefix(executable, `\\`) {
		return false
	}
	if len(executable) >= 3 && executable[1] == ':' && (executable[2] == '\\' || executable[2] == '/') {
		return false
	}
	// Step labels are optional and weak evidence. Test intent must occupy an
	// executable/module/target position, not merely occur in an argument: for
	// example `python -m pip install tox` provisions a runner but executes zero
	// tests and must fall through to tox.ini.
	return commandRunsTests(fields)
}

// commandRunsTests recognizes test execution only in command-bearing positions.
// This deliberately rejects setup commands whose arguments merely mention test
// dependencies, such as `pip install .[test]`.
func commandRunsTests(fields []string) bool {
	for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "=") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return false
	}
	executable := commandToken(fields[0])
	if testIntentToken(executable) {
		return true
	}
	switch {
	case strings.HasPrefix(executable, "python"), strings.HasPrefix(executable, "pypy"):
		for index := 1; index < len(fields); index++ {
			if fields[index] == "-m" && index+1 < len(fields) {
				return testIntentToken(commandToken(fields[index+1]))
			}
			if !strings.HasPrefix(fields[index], "-") {
				return testIntentToken(commandToken(fields[index]))
			}
		}
	case executable == "make", executable == "gmake", executable == "just",
		executable == "npm", executable == "yarn", executable == "pnpm",
		executable == "bun", executable == "go", executable == "cargo":
		for _, field := range fields[1:] {
			if !strings.HasPrefix(field, "-") {
				return testIntentToken(commandToken(field))
			}
		}
	case executable == "uv", executable == "poetry", executable == "pipenv", executable == "hatch":
		for index := 1; index < len(fields); index++ {
			if commandToken(fields[index]) == "run" {
				return commandRunsTests(fields[index+1:])
			}
		}
	case executable == "coverage":
		for index := 1; index < len(fields); index++ {
			if commandToken(fields[index]) != "run" {
				continue
			}
			tail := fields[index+1:]
			if len(tail) >= 2 && tail[0] == "-m" {
				return testIntentToken(commandToken(tail[1]))
			}
			return commandRunsTests(tail)
		}
	}
	return false
}

func commandToken(field string) string {
	token := strings.ToLower(strings.Trim(field, `"'`))
	token = strings.ReplaceAll(token, `\\`, "/")
	if slash := strings.LastIndex(token, "/"); slash >= 0 {
		token = token[slash+1:]
	}
	return token
}

func testIntentToken(token string) bool {
	return strings.Contains(token, "test") || strings.Contains(token, "check") || token == "tox" || token == "nox"
}

func extractFromReadme(d declaration) (string, bool) {
	lines := strings.Split(d.text, "\n")
	near := false
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		low := strings.ToLower(trimmed)
		cand := strings.TrimSpace(strings.TrimPrefix(trimmed, "$"))
		if near && cand != "" && !strings.HasPrefix(cand, "```") &&
			looksLikeCommand(cand) && commandRunsTests(strings.Fields(cand)) {
			return cand, true
		}
		if strings.Contains(low, "test") &&
			(strings.HasPrefix(trimmed, "#") || strings.HasSuffix(trimmed, ":") ||
				strings.Contains(low, "run the test") || isHeadingLike(trimmed)) {
			near = true
			continue
		}
		if !near {
			continue
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

// ── provisioning derivation (NEW; uv/python knowledge belongs here) ──

// deriveProvisioning reads the project's packaging metadata and produces the uv
// provisioning map SpecFromFormula consumes: no_project + editable install,
// python version (pyproject requires-python / .python-version), requirement
// files (requirements*.txt, requirements/*.txt), and declared test extras/groups
// (PEP 621/735 or setuptools setup.cfg). Best-effort: every field is optional
// and the tooling inner loop heals what derivation can't see.
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
	// Resolve dependencies from the package universe that existed when this
	// source revision was committed. Historical projects routinely leave build
	// tools loosely constrained; selecting today's newest setuptools broke a
	// 2022 Astropy checkout after setuptools removed an API its build used.
	// uv owns the resolver and exposes this exact temporal constraint.
	if committedAt, ok := repositoryCommitTime(dir); ok {
		prov["exclude_newer"] = committedAt.UTC().Format(time.RFC3339)
	}
	if reqs := deriveRequirementFiles(dir); len(reqs) > 0 {
		prov["requirements"] = strings.Join(reqs, ",")
	}
	// A PEP 517 build backend owns both its static [build-system].requires and
	// the dynamic requirements returned by hooks such as
	// get_requires_for_build_editable. Keep build isolation enabled so uv can
	// honor that complete standard contract. Persist the resulting editable
	// environment so expensive builds still happen once per workspace. Explicit
	// recovery configuration may opt out of isolation later; derivation must not
	// guess that every non-setuptools backend is a C-extension project.
	if declaresPEP517BuildSystem(dir) {
		prov["persistent_venv"] = "true"
	}
	// Test tooling is often deliberately absent from the installable package's
	// runtime dependencies. Preserve that distinction and ask uv to materialize
	// the project's declared test/dev dependency groups and optional extras. The
	// names come from packaging structure; uv remains the dependency implementation.
	groups, extras := deriveTestDependencySets(dir)
	if len(groups) > 0 {
		prov["dependency_groups"] = strings.Join(groups, ",")
		prov["persistent_venv"] = "true"
	}
	if len(extras) > 0 {
		prov["extras"] = strings.Join(extras, ",")
		prov["persistent_venv"] = "true"
	}
	return prov
}

// pyprojectDependencySets is the small standards-owned slice of pyproject.toml
// needed for test-environment provisioning. Dependency values intentionally
// remain opaque: Codefly chooses declared group/extra NAMES and uv resolves
// their PEP 508/735 contents, including nested group includes.
type pyprojectDependencySets struct {
	DependencyGroups map[string]any `toml:"dependency-groups"`
	Project          struct {
		OptionalDependencies map[string]any `toml:"optional-dependencies"`
	} `toml:"project"`
}

// deriveTestDependencySets returns declared dependency sets whose names carry
// test/development intent. This is naming-policy, not package inference: no
// dependency or framework name is inspected or invented.
func deriveTestDependencySets(dir string) (groups, extras []string) {
	groupNames := map[string]struct{}{}
	extraNames := map[string]struct{}{}
	if payload, err := os.ReadFile(filepath.Join(dir, "pyproject.toml")); err == nil {
		var project pyprojectDependencySets
		if toml.Unmarshal(payload, &project) == nil {
			for name := range project.DependencyGroups {
				groupNames[name] = struct{}{}
			}
			for name := range project.Project.OptionalDependencies {
				extraNames[name] = struct{}{}
			}
		}
	}
	for _, name := range setupCfgExtraNames(dir) {
		extraNames[name] = struct{}{}
	}
	return declaredTestDependencySetNames(groupNames), preferredTestExtraNames(extraNames)
}

// setupCfgExtraNames reads only option names from setuptools' declarative
// [options.extras_require] section. Dependency values remain opaque and uv
// resolves them through the selected extra. Supporting this current setuptools
// contract is essential for historical projects that predate PEP 621 but still
// declare a complete test environment.
func setupCfgExtraNames(dir string) []string {
	payload, err := os.ReadFile(filepath.Join(dir, "setup.cfg"))
	if err != nil {
		return nil
	}
	inExtras := false
	names := map[string]struct{}{}
	for _, raw := range strings.Split(string(payload), "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inExtras = strings.EqualFold(strings.TrimSpace(trimmed[1:len(trimmed)-1]), "options.extras_require")
			continue
		}
		if !inExtras || trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		// Indented lines are requirement continuations, not option names.
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		if name, _, ok := iniKey(trimmed); ok && name != "" {
			names[name] = struct{}{}
		}
	}
	all := make([]string, 0, len(names))
	for name := range names {
		all = append(all, name)
	}
	sort.Strings(all)
	return all
}

func declaredTestDependencySetNames(names map[string]struct{}) []string {
	selected := make([]string, 0, len(names))
	for name := range names {
		if testDependencySetName(name) {
			selected = append(selected, name)
		}
	}
	sort.Strings(selected)
	return selected
}

// preferredTestExtraNames chooses declared extra names, never individual
// packages. When a project offers a focused conventional test extra alongside
// broader sets such as test_all or development, the focused set is
// authoritative; installing every broader extra makes narrow test execution
// slower and can introduce unrelated dependency conflicts. Dependency groups
// intentionally retain all matching groups because PEP 735 groups may include
// one another and do not have setuptools' common test/test_all convention.
func preferredTestExtraNames(names map[string]struct{}) []string {
	var focused, fallback []string
	for name := range names {
		if !testDependencySetName(name) {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "test", "tests", "testing":
			focused = append(focused, name)
		default:
			fallback = append(fallback, name)
		}
	}
	if len(focused) > 0 {
		sort.Strings(focused)
		return focused
	}
	sort.Strings(fallback)
	return fallback
}

// testDependencySetName recognizes conventional intent tokens without fuzzy
// substring matching (for example, "device" is not a dev group). Separators
// allow names such as test-dependencies and docs_and_tests.
func testDependencySetName(name string) bool {
	tokens := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(name)), func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/' || r == ' '
	})
	for _, token := range tokens {
		switch token {
		case "dev", "development", "test", "tests", "testing":
			return true
		}
	}
	return false
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

// declaresPEP517BuildSystem reports whether pyproject.toml selects a standard
// build backend. We only need the section identity: uv remains the TOML and
// packaging implementation and resolves the section's static and dynamic
// requirements itself.
func declaresPEP517BuildSystem(dir string) bool {
	py := readFileString(filepath.Join(dir, "pyproject.toml"))
	for _, line := range strings.Split(py, "\n") {
		if strings.TrimSpace(line) == "[build-system]" {
			return true
		}
	}
	return false
}

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

// oldestManagedPython is the floor available from uv's managed standalone
// interpreter catalog. Commit-date inference cannot select an older runtime:
// doing so produces an impossible --python contract instead of a runnable
// historical approximation. Explicit project pins remain authoritative and
// fail visibly when the requested interpreter is unavailable.
const oldestManagedPython = "3.8"

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
}

func releaseDate(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

// inferPythonFromCommitDate returns the newest CPython minor that had GA'd on or
// before the repo's HEAD commit date, or "" when the date can't be read (not a
// git repo / git unavailable). This is the "don't go forward in time" rule: a
// repo committed in 2022 should run on a 2022-or-earlier interpreter, not 3.13.
func inferPythonFromCommitDate(dir string) string {
	t, ok := repositoryCommitTime(dir)
	if !ok {
		return ""
	}
	for _, r := range pythonReleases { // newest first
		if !t.Before(r.ga) {
			return r.version
		}
	}
	return oldestManagedPython
}

func repositoryCommitTime(dir string) (time.Time, bool) {
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%cI", "HEAD").Output()
	if err != nil {
		return time.Time{}, false
	}
	committedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
	if err != nil {
		return time.Time{}, false
	}
	return committedAt, true
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
