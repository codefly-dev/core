package code

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// PythonCodeServer extends DefaultCodeServer with Python-specific operations:
// GetProjectInfo (pyproject.toml + uv) and ListDependencies (uv pip list).
type PythonCodeServer struct {
	*DefaultCodeServer
}

// NewPythonCodeServer creates a Python-aware code server.
func NewPythonCodeServer(dir string, serverOpts []ServerOption) *PythonCodeServer {
	base := NewDefaultCodeServer(dir, serverOpts...)
	s := &PythonCodeServer{
		DefaultCodeServer: base,
	}
	s.registerPythonOverrides()
	return s
}

func (s *PythonCodeServer) registerPythonOverrides() {
	s.Override("get_project_info", s.handleGetProjectInfo)
	s.Override("list_dependencies", s.handleListDependencies)
}

// --- Standalone gRPC RPCs ---

func (s *PythonCodeServer) GetProjectInfo(ctx context.Context, req *codev0.GetProjectInfoRequest) (*codev0.GetProjectInfoResponse, error) {
	resp, err := s.handleGetProjectInfo(ctx, nil)
	if err != nil {
		return nil, err
	}
	return resp.GetGetProjectInfo(), nil
}

func (s *PythonCodeServer) ListDependencies(ctx context.Context, req *codev0.ListDependenciesRequest) (*codev0.ListDependenciesResponse, error) {
	resp, err := s.handleListDependencies(ctx, nil)
	if err != nil {
		return nil, err
	}
	return resp.GetListDependencies(), nil
}

// --- Handlers ---

func (s *PythonCodeServer) handleGetProjectInfo(_ context.Context, _ *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	srcDir := s.SourceDir
	resp := &codev0.GetProjectInfoResponse{Language: "python"}

	var manifestDeps []*codev0.Dependency
	// Parse pyproject.toml for module name, Python version, AND
	// declared dependencies. Manifest-declared deps land first;
	// uv pip list output (if available) merges in next and adds
	// transitive deps + resolved versions.
	data, err := s.FS.ReadFile(filepath.Join(srcDir, "pyproject.toml"))
	if err == nil {
		resp.Module, resp.LanguageVersion = parsePyprojectTOML(string(data))
		manifestDeps = parsePyprojectDependencies(string(data))
	}

	// Discover packages (directories with __init__.py)
	resp.Packages = s.discoverPackages(srcDir)

	// File hashes
	resp.FileHashes = s.computeFileHashes(srcDir)

	// Merge: manifest deps (declared, source of truth for direct
	// deps) + uv pip list (resolved, includes transitives). Dedup
	// by name; manifest wins on conflicts because it carries the
	// caller's declared version constraint.
	seen := map[string]bool{}
	for _, d := range manifestDeps {
		if d == nil || d.Name == "" || seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		resp.Dependencies = append(resp.Dependencies, d)
	}
	for _, d := range s.listUVDependencies(srcDir) {
		if d == nil || d.Name == "" || seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		resp.Dependencies = append(resp.Dependencies, d)
	}

	return wrapProjectInfoPython(resp), nil
}

func (s *PythonCodeServer) handleListDependencies(_ context.Context, _ *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	srcDir := s.SourceDir
	deps := s.listUVDependencies(srcDir)
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_ListDependencies{
		ListDependencies: &codev0.ListDependenciesResponse{Dependencies: deps},
	}}, nil
}

// --- Helpers ---

func parsePyprojectTOML(content string) (module, pythonVersion string) {
	// Simple line-based parsing (avoid TOML dependency for now)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name") {
			if v := extractTOMLValue(line); v != "" {
				module = v
			}
		}
		if strings.HasPrefix(line, "requires-python") {
			if v := extractTOMLValue(line); v != "" {
				pythonVersion = v
			}
		}
	}
	return
}

func extractTOMLValue(line string) string {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return ""
	}
	v := strings.TrimSpace(parts[1])
	v = strings.Trim(v, `"'`)
	return v
}

// parsePyprojectDependencies extracts declared deps from the
// `[project] dependencies = [...]` table and every
// `[project.optional-dependencies] <group> = [...]` block.
// Direct=true for both — they're explicitly named by the
// project author (vs transitives that uv pip list discovers).
//
// PEP 621 format example:
//
//	[project]
//	dependencies = [
//	    "flask>=2.0",
//	    "requests",
//	    "numpy~=1.20",
//	]
//	[project.optional-dependencies]
//	dev = ["pytest", "black"]
//
// Each entry is "<name><spec>" where spec is a PEP 440
// version constraint (>=, ==, ~=, !=, <, >, ===) or empty.
// Strip the spec to get the name; keep the spec as Version.
//
// Line-based parsing — same philosophy as parsePyprojectTOML
// (no TOML dependency, accepts a small accuracy hit on weird
// formattings for the win of zero new deps).
func parsePyprojectDependencies(content string) []*codev0.Dependency {
	var out []*codev0.Dependency
	seen := map[string]bool{}
	lines := strings.Split(content, "\n")

	// State machine: when we see `dependencies = [` or
	// `optional-dependencies.<group> = [`, switch into a
	// reading mode until the next `]`. PEP 621 dependency
	// lists are always inline-array-of-strings; we don't
	// support multi-line nested tables (PEP 631 sub-spec).
	const (
		modeIdle = iota
		modeReadList
	)
	mode := modeIdle
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if mode == modeIdle {
			if strings.HasPrefix(line, "dependencies") {
				// Either `dependencies = [...]` (single-line) or
				// `dependencies = [` (multi-line). Strip up to
				// the `[` and re-process the remainder as if
				// it were inside the list.
				if i := strings.IndexByte(line, '['); i >= 0 {
					mode = modeReadList
					line = strings.TrimSpace(line[i+1:])
				} else {
					continue
				}
			} else if strings.HasPrefix(line, "[project.optional-dependencies]") {
				// Header — subsequent lines like
				// `dev = ["pytest", "black"]` need parsing.
				// Fall through; the next iteration will pick
				// them up as `dev = [` patterns.
				continue
			} else if strings.Contains(line, "= [") &&
				!strings.HasPrefix(line, "[") {
				// Likely an optional-dependencies group entry
				// (`dev = [...]`). Handle the same way as the
				// main `dependencies = [` case.
				if i := strings.IndexByte(line, '['); i >= 0 {
					mode = modeReadList
					line = strings.TrimSpace(line[i+1:])
				} else {
					continue
				}
			} else {
				continue
			}
		}
		// In modeReadList: extract quoted strings from this
		// line, then check for `]` end marker.
		for {
			depSpec, rest, found := nextTOMLString(line)
			if !found {
				break
			}
			name, version := splitPEP440Spec(depSpec)
			if name != "" && !seen[name] {
				seen[name] = true
				out = append(out, &codev0.Dependency{
					Name: name, Version: version, Direct: true,
				})
			}
			line = rest
		}
		if strings.Contains(line, "]") {
			mode = modeIdle
		}
	}
	return out
}

// nextTOMLString peels the next quoted string off `line`,
// returning the unquoted content + the unread remainder + a
// found flag.
func nextTOMLString(line string) (value, rest string, found bool) {
	// Find first ' or " quote.
	openIdx := -1
	var quote byte
	for i := 0; i < len(line); i++ {
		if line[i] == '"' || line[i] == '\'' {
			openIdx = i
			quote = line[i]
			break
		}
	}
	if openIdx < 0 {
		return "", "", false
	}
	closeIdx := strings.IndexByte(line[openIdx+1:], quote)
	if closeIdx < 0 {
		return "", "", false
	}
	return line[openIdx+1 : openIdx+1+closeIdx], line[openIdx+1+closeIdx+1:], true
}

// splitPEP440Spec splits "flask>=2.0" → ("flask", ">=2.0").
// Recognizes the standard PEP 440 operators. Handles "flask"
// (no version) → ("flask", "").
func splitPEP440Spec(spec string) (name, version string) {
	spec = strings.TrimSpace(spec)
	// PEP 440 operators (longest-first so === matches before ==).
	ops := []string{"===", "==", "!=", "~=", ">=", "<=", ">", "<"}
	for _, op := range ops {
		if i := strings.Index(spec, op); i > 0 {
			return strings.TrimSpace(spec[:i]), strings.TrimSpace(spec[i:])
		}
	}
	// Also strip PEP 508 extras: `requests[security]>=2.0` → name
	// `requests`. We do this after operator detection so
	// `requests[security]` (no version) still trims correctly.
	if i := strings.IndexByte(spec, '['); i > 0 {
		return strings.TrimSpace(spec[:i]), ""
	}
	if i := strings.IndexByte(spec, ';'); i > 0 {
		// Environment marker: `numpy; python_version >= "3.8"`.
		return strings.TrimSpace(spec[:i]), ""
	}
	return spec, ""
}

func (s *PythonCodeServer) discoverPackages(srcDir string) []*codev0.PackageInfo {
	var packages []*codev0.PackageInfo

	entries, err := s.FS.ReadDir(srcDir)
	if err != nil {
		return packages
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		// Skip common non-package dirs
		skip := map[string]bool{"venv": true, ".venv": true, "node_modules": true, "__pycache__": true, ".git": true}
		if skip[name] {
			continue
		}

		// Check for __init__.py or .py files
		initPath := filepath.Join(srcDir, name, "__init__.py")
		if _, err := s.FS.Stat(initPath); err != nil {
			// Also accept directories with .py files (namespace packages)
			subEntries, err := s.FS.ReadDir(filepath.Join(srcDir, name))
			if err != nil {
				continue
			}
			hasPy := false
			for _, se := range subEntries {
				if strings.HasSuffix(se.Name(), ".py") {
					hasPy = true
					break
				}
			}
			if !hasPy {
				continue
			}
		}

		pkg := &codev0.PackageInfo{
			Name:         name,
			RelativePath: name,
		}

		// List .py files
		subEntries, _ := s.FS.ReadDir(filepath.Join(srcDir, name))
		for _, se := range subEntries {
			if strings.HasSuffix(se.Name(), ".py") {
				pkg.Files = append(pkg.Files, se.Name())
			}
		}

		// Scan each file for import statements and aggregate at
		// the package level. Heuristic line-based parser — same
		// style as parsePyprojectTOML above. Avoids a Python
		// runtime dependency at the cost of some edge cases
		// (imports inside triple-quoted strings will false-match).
		pkg.Imports = s.scanPackageImports(srcDir, name, pkg.Files)

		packages = append(packages, pkg)
	}

	// Also check root-level .py files
	rootPkg := &codev0.PackageInfo{
		Name:         ".",
		RelativePath: ".",
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".py") {
			rootPkg.Files = append(rootPkg.Files, e.Name())
		}
	}
	if len(rootPkg.Files) > 0 {
		rootPkg.Imports = s.scanPackageImports(srcDir, ".", rootPkg.Files)
		packages = append(packages, rootPkg)
	}

	return packages
}

// scanPackageImports walks each .py file in the package and
// extracts the set of imported modules. Returns a deduplicated,
// sorted list of import paths AT THE TOP-LEVEL DOTTED COMPONENT
// (the canonical "dependency name" — e.g. `flask` from `flask.blueprints`).
//
// This is a heuristic line-based parser, not a real Python AST
// walk. Matches:
//
//	import X
//	import X.Y
//	import X as A
//	import X, Y, Z
//	from X import a, b
//	from X.Y import z
//
// Known false-matches: `import` keywords inside triple-quoted
// strings or comments. Acceptable noise for the dependency-view
// use case; consumers downstream filter stdlib / internal anyway.
//
// Relative imports (`from . import x`, `from .pkg import x`) are
// skipped — they're always internal-to-package.
func (s *PythonCodeServer) scanPackageImports(srcDir, pkgRel string, files []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(module string) {
		module = strings.TrimSpace(module)
		// Strip alias: `os as o` → `os`. The `as` keyword is the
		// only thing legal after the module in `import X as Y`.
		if i := strings.Index(module, " as "); i > 0 {
			module = module[:i]
		}
		module = strings.TrimSpace(module)
		// Top-level dotted component: `flask.blueprints` → `flask`.
		if dot := strings.IndexByte(module, '.'); dot > 0 {
			module = module[:dot]
		}
		if module == "" || seen[module] {
			return
		}
		seen[module] = true
		out = append(out, module)
	}
	for _, file := range files {
		path := filepath.Join(srcDir, pkgRel, file)
		if pkgRel == "." {
			path = filepath.Join(srcDir, file)
		}
		data, err := s.FS.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			// `from X import ...` — handle before `import` so the
			// stricter prefix wins. Skip relative imports.
			if rest, ok := strings.CutPrefix(line, "from "); ok {
				space := strings.IndexAny(rest, " \t")
				if space <= 0 {
					continue
				}
				module := rest[:space]
				if strings.HasPrefix(module, ".") {
					continue // relative import
				}
				add(module)
				continue
			}
			if rest, ok := strings.CutPrefix(line, "import "); ok {
				// May be comma-separated: `import os, sys`.
				// Strip trailing `# comment` if present.
				if hash := strings.IndexByte(rest, '#'); hash >= 0 {
					rest = rest[:hash]
				}
				for _, mod := range strings.Split(rest, ",") {
					add(mod)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func (s *PythonCodeServer) computeFileHashes(srcDir string) map[string]string {
	hashes := make(map[string]string)
	filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Only hash .py and pyproject.toml
		if !strings.HasSuffix(path, ".py") && !strings.HasSuffix(path, "pyproject.toml") {
			return nil
		}
		// Skip venvs and caches
		for _, skip := range []string{".venv", "venv", "__pycache__", ".git"} {
			if strings.Contains(path, skip) {
				return nil
			}
		}
		rel, _ := filepath.Rel(srcDir, path)
		data, err := s.FS.ReadFile(path)
		if err == nil {
			h := sha256.Sum256(data)
			hashes[rel] = fmt.Sprintf("%x", h)
		}
		return nil
	})
	return hashes
}

type uvPkgInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (s *PythonCodeServer) listUVDependencies(srcDir string) []*codev0.Dependency {
	// Try uv pip list --format json
	cmd := exec.Command("uv", "pip", "list", "--format", "json")
	cmd.Dir = srcDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		return nil
	}

	var pkgs []uvPkgInfo
	if err := json.Unmarshal(out.Bytes(), &pkgs); err != nil {
		return nil
	}

	// Read pyproject.toml to determine direct deps
	directDeps := make(map[string]bool)
	data, err := s.FS.ReadFile(filepath.Join(srcDir, "pyproject.toml"))
	if err == nil {
		directDeps = extractDirectDeps(string(data))
	}

	var deps []*codev0.Dependency
	for _, pkg := range pkgs {
		deps = append(deps, &codev0.Dependency{
			Name:    pkg.Name,
			Version: pkg.Version,
			Direct:  directDeps[strings.ToLower(pkg.Name)],
		})
	}
	return deps
}

func extractDirectDeps(content string) map[string]bool {
	deps := make(map[string]bool)
	inDeps := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "dependencies = [" || trimmed == "dependencies= [" {
			inDeps = true
			continue
		}
		if inDeps && trimmed == "]" {
			break
		}
		if inDeps {
			// Parse "package>=version" or "package"
			dep := strings.Trim(trimmed, `"', `)
			// Remove version specifiers
			for _, sep := range []string{">=", "<=", "==", "!=", "~=", "<", ">"} {
				if idx := strings.Index(dep, sep); idx > 0 {
					dep = dep[:idx]
				}
			}
			// Remove extras [...]
			if idx := strings.Index(dep, "["); idx > 0 {
				dep = dep[:idx]
			}
			dep = strings.TrimSpace(dep)
			if dep != "" {
				deps[strings.ToLower(dep)] = true
			}
		}
	}
	return deps
}

func wrapProjectInfoPython(resp *codev0.GetProjectInfoResponse) *codev0.CodeResponse {
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetProjectInfo{GetProjectInfo: resp}}
}
