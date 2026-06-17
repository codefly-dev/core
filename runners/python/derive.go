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
	"path/filepath"
	"regexp"
	"strings"
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
	return argv, output, nil, deriveProvisioning(sourceDir), true
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
	prov := map[string]string{"no_project": "true", "editable": "true"}
	if v := derivePythonVersion(dir); v != "" {
		prov["python"] = v
	}
	if reqs := deriveRequirementFiles(dir); len(reqs) > 0 {
		prov["requirements"] = strings.Join(reqs, ",")
	}
	// NOTE: pyproject [project.optional-dependencies] test/dev extras (`.[test]`)
	// are a known gap — SpecFromFormula has no `--extra` flag yet. When a project
	// needs them, the tooling inner loop heals provisioning until the env runs;
	// add `extras` support to SpecFromFormula to derive them up front.
	return prov
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
			// requires-python is a constraint (">=3.9"); take the floor version.
			if v := rePyVerNum.FindString(m[1]); v != "" {
				return v
			}
		}
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
			if !e.IsDir() && strings.HasSuffix(n, ".txt") &&
				(strings.Contains(n, "test") || strings.Contains(n, "dev")) {
				out = append(out, filepath.Join("requirements", n))
			}
		}
	}
	return out
}
