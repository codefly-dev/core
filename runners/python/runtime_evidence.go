package python

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const runtimeEvidenceHeader = "Python runtime evidence:"

type runtimeSource struct {
	Kind string
	Path string
}

// RuntimeEvidence reports the Python runner/environment facts detected from a
// code unit. It only reports files that actually exist under sourceDir.
func RuntimeEvidence(sourceDir string) string {
	cmd, output, env, prov, ok := DeriveFormula(sourceDir)
	return RuntimeEvidenceForFormula(sourceDir, cmd, output, env, prov, ok)
}

// RuntimeEvidenceForFormula reports the Python runner/environment facts for an
// already resolved formula plus the project-level sources detected under
// sourceDir.
func RuntimeEvidenceForFormula(sourceDir string, cmd []string, output string, env, prov map[string]string, derived bool) string {
	var b strings.Builder
	b.WriteString(runtimeEvidenceHeader + "\n")
	b.WriteString("  language: python\n")
	b.WriteString("  runner_environment_manager: uv\n")
	b.WriteString("  project_environment_manager: " + detectProjectEnvironmentManager(sourceDir) + "\n")
	if derived {
		b.WriteString("  formula_source: derived from project declarations\n")
	} else if len(cmd) > 0 {
		b.WriteString("  formula_source: supplied runtime formula\n")
	} else {
		b.WriteString("  formula_source: not detected\n")
	}
	if len(cmd) > 0 {
		b.WriteString("  test_command: " + strings.Join(cmd, " ") + "\n")
	} else {
		b.WriteString("  test_command: (not detected)\n")
	}
	if strings.TrimSpace(output) != "" {
		b.WriteString("  test_output: " + strings.TrimSpace(output) + "\n")
	}
	// The run directory matters as much as the command (django's runtests.py
	// only works from tests/) — surface it explicitly so a healer sees WHERE
	// the command runs, not just what it is.
	if cwd := strings.TrimSpace(prov["cwd"]); cwd != "" {
		b.WriteString("  test_cwd: " + cwd + " (relative to the code unit)\n")
	} else {
		b.WriteString("  test_cwd: . (code unit root)\n")
	}
	if len(prov) > 0 {
		b.WriteString("  provisioning:\n")
		for _, line := range sortedMapLines(prov) {
			b.WriteString("    " + line + "\n")
		}
	}
	if len(env) > 0 {
		b.WriteString("  env:\n")
		for _, line := range sortedMapLines(env) {
			b.WriteString("    " + line + "\n")
		}
	}
	if len(cmd) > 0 {
		spec := SpecFromFormula(cmd, output, env, prov, nil)
		if spec.NoBuildIsolation {
			if requirements, err := readBuildSystemRequirements(sourceDir); err == nil && len(requirements) > 0 {
				b.WriteString("  pep517_static_build_requirements: " + strings.Join(requirements, ", ") + " (materialized automatically)\n")
			}
		}
		// RunFormulaStructured resolves editable installs to the absolute source
		// root before spawning uv. Render the same semantic target without leaking
		// a machine-specific temporary workspace path into logs/cassettes. A bare
		// "." is actively misleading when cwd=tests because it appears to target
		// the run directory rather than the code-unit root.
		if spec.Editable {
			spec.EditableTarget = "<code-unit-root>"
		}
		b.WriteString("  uv_args: uv " + strings.Join(BuildUvArgs(spec, ""), " ") + "\n")
	}
	sources := detectRuntimeSources(sourceDir)
	if len(sources) > 0 {
		b.WriteString("  detected_config_sources:\n")
		for _, src := range sources {
			b.WriteString("    - " + src.Kind + ": " + src.Path + "\n")
		}
	}
	// ARCHITECTURE: This is the model-visible configuration contract at the
	// project/runtime boundary. Mind must stay language-agnostic, so the Python
	// plugin owns both the vocabulary and the value semantics. A bare key list
	// proved too weak: callers confused requirement FILES with package SPECS and
	// used editable=false as a build workaround even though it removes the
	// project from the test environment. Keep the contract beside the formula
	// evidence so every typed environment block is self-describing.
	b.WriteString(`  settable_configuration:
    test.provisioning.python: SET a CPython version (for example "3.10"); UNSET restores project derivation
    test.provisioning.exclude_newer: SET an RFC3339 dependency publication cutoff; UNSET restores project derivation
    test.provisioning.editable: SET true to install the code-unit root editable; false deliberately omits that source install; UNSET restores project derivation
    test.provisioning.no_project: SET true or false; UNSET restores project derivation
    test.provisioning.requirements: SET or APPEND code-unit-relative requirement FILE paths (for example "requirements/test.txt"), never package specs
    test.provisioning.with: SET or APPEND Python package requirement SPECS (for example "pytest" or "Werkzeug<3"), never paths or placeholders
    test.provisioning.dependency_groups: SET or APPEND project-declared dependency group names
    test.provisioning.extras: SET or APPEND project-declared extra names
    test.provisioning.no_build_isolation: SET true when an editable PEP 517 build cannot import its declared build backend/requirements; the plugin materializes declared build requirements; UNSET restores project derivation
    test.provisioning.persistent_venv: SET true or false; UNSET restores project derivation
    test.provisioning.cwd: SET a code-unit-relative existing test working directory; UNSET restores project derivation
    test.env.<NAME>: SET a diagnostic-proven environment variable (for example test.env.CFLAGS); UNSET removes the override
  recovery_policy: preserve the project source install, change only the smallest diagnostic-supported setting, probe after each atomic repair, and UNSET disproven experiments
`)
	return strings.TrimRight(b.String(), "\n")
}

// AppendRuntimeEvidence appends evidence once to a status/detail message.
func AppendRuntimeEvidence(message, evidence string) string {
	message = strings.TrimSpace(message)
	evidence = strings.TrimSpace(evidence)
	if evidence == "" || strings.Contains(message, runtimeEvidenceHeader) {
		return message
	}
	if message == "" {
		return evidence
	}
	return message + "\n\n" + evidence
}

func sortedMapLines(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("%s: %s", key, values[key]))
	}
	return lines
}

func detectProjectEnvironmentManager(sourceDir string) string {
	pyproject := readSourceFile(sourceDir, "pyproject.toml")
	switch {
	case sourceFileExists(sourceDir, "uv.lock") || strings.Contains(pyproject, "[tool.uv]"):
		return "uv"
	case sourceFileExists(sourceDir, "poetry.lock") || strings.Contains(pyproject, "[tool.poetry]"):
		return "poetry"
	case sourceFileExists(sourceDir, "Pipfile"):
		return "pipenv"
	case strings.TrimSpace(pyproject) != "":
		return "pyproject"
	case len(requirementSourcePaths(sourceDir)) > 0:
		return "requirements"
	case sourceFileExists(sourceDir, "setup.py") || sourceFileExists(sourceDir, "setup.cfg"):
		return "setuptools"
	default:
		return "unknown"
	}
}

func detectRuntimeSources(sourceDir string) []runtimeSource {
	var sources []runtimeSource
	add := func(kind, rel string) {
		if sourceFileExists(sourceDir, rel) {
			sources = append(sources, runtimeSource{Kind: kind, Path: rel})
		}
	}
	for _, rel := range workflowSourcePaths(sourceDir) {
		sources = append(sources, runtimeSource{Kind: "test command declaration", Path: rel})
	}
	for _, rel := range []string{"tox.ini", "Makefile", "GNUmakefile", "noxfile.py", "CONTRIBUTING.md", "CONTRIBUTING.rst", "README.md", "README.rst"} {
		add("test command declaration", rel)
	}
	for _, rel := range []string{".python-version", "pyproject.toml", "setup.cfg", "setup.py", "Pipfile", "poetry.lock", "uv.lock"} {
		add("python project/environment declaration", rel)
	}
	for _, rel := range requirementSourcePaths(sourceDir) {
		sources = append(sources, runtimeSource{Kind: "python dependency declaration", Path: rel})
	}
	sort.SliceStable(sources, func(i, j int) bool {
		if sources[i].Kind != sources[j].Kind {
			return sources[i].Kind < sources[j].Kind
		}
		return sources[i].Path < sources[j].Path
	})
	return dedupeRuntimeSources(sources)
}

func workflowSourcePaths(sourceDir string) []string {
	dir := filepath.Join(sourceDir, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext == ".yml" || ext == ".yaml" {
			out = append(out, filepath.ToSlash(filepath.Join(".github", "workflows", entry.Name())))
		}
	}
	sort.Strings(out)
	return out
}

func requirementSourcePaths(sourceDir string) []string {
	var out []string
	for _, pattern := range []string{
		filepath.Join(sourceDir, "requirements*.txt"),
		filepath.Join(sourceDir, "requirements", "*.txt"),
		filepath.Join(sourceDir, "constraints*.txt"),
	} {
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			if info, err := os.Stat(match); err == nil && !info.IsDir() {
				if rel, err := filepath.Rel(sourceDir, match); err == nil {
					out = append(out, filepath.ToSlash(rel))
				}
			}
		}
	}
	sort.Strings(out)
	return compactStrings(out)
}

func dedupeRuntimeSources(in []runtimeSource) []runtimeSource {
	seen := map[string]bool{}
	out := make([]runtimeSource, 0, len(in))
	for _, src := range in {
		key := src.Kind + "\x00" + src.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, src)
	}
	return out
}

func compactStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func sourceFileExists(sourceDir, rel string) bool {
	info, err := os.Stat(filepath.Join(sourceDir, filepath.FromSlash(rel)))
	return err == nil && !info.IsDir()
}

func readSourceFile(sourceDir, rel string) string {
	b, err := os.ReadFile(filepath.Join(sourceDir, filepath.FromSlash(rel))) //nolint:gosec // path is a fixed source-relative probe.
	if err != nil {
		return ""
	}
	return string(b)
}
