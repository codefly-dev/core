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
	"strings"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// PythonCodeServer extends DefaultCodeServer with Python-specific operations:
// GetProjectInfo (pyproject.toml + uv), ListDependencies (uv pip list),
// and ListSymbols (via pluggable SymbolProvider — AST or LSP).
type PythonCodeServer struct {
	*DefaultCodeServer
	symbols SymbolProvider
}

// PythonServerOption configures a PythonCodeServer.
type PythonServerOption func(*PythonCodeServer)

// WithPythonSymbolProvider overrides the default symbol provider.
func WithPythonSymbolProvider(sp SymbolProvider) PythonServerOption {
	return func(s *PythonCodeServer) {
		s.symbols = sp
	}
}

// NewPythonCodeServer creates a Python-aware code server.
func NewPythonCodeServer(dir string, serverOpts []ServerOption, pyOpts ...PythonServerOption) *PythonCodeServer {
	base := NewDefaultCodeServer(dir, serverOpts...)
	s := &PythonCodeServer{
		DefaultCodeServer: base,
		symbols:           NewPythonASTSymbolProvider(dir),
	}
	for _, o := range pyOpts {
		o(s)
	}
	s.registerPythonOverrides()
	return s
}

func (s *PythonCodeServer) registerPythonOverrides() {
	s.Override("get_project_info", s.handleGetProjectInfo)
	s.Override("list_dependencies", s.handleListDependencies)
	if s.symbols != nil {
		s.Override("list_symbols", s.handleListSymbols)
	}
}

// --- Standalone gRPC RPCs ---

func (s *PythonCodeServer) GetProjectInfo(ctx context.Context, req *codev0.GetProjectInfoRequest) (*codev0.GetProjectInfoResponse, error) {
	resp, err := s.handleGetProjectInfo(ctx, nil)
	if err != nil {
		return nil, err
	}
	return resp.GetGetProjectInfo(), nil
}

func (s *PythonCodeServer) ListSymbols(ctx context.Context, req *codev0.ListSymbolsRequest) (*codev0.ListSymbolsResponse, error) {
	if s.symbols == nil {
		return &codev0.ListSymbolsResponse{
			Status: &codev0.ListSymbolsStatus{State: codev0.ListSymbolsStatus_ERROR, Message: "no symbol provider configured"},
		}, nil
	}
	codeReq := &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ListSymbols{ListSymbols: req},
	}
	resp, err := s.handleListSymbols(ctx, codeReq)
	if err != nil {
		return nil, err
	}
	return resp.GetListSymbols(), nil
}

func (s *PythonCodeServer) ListDependencies(ctx context.Context, req *codev0.ListDependenciesRequest) (*codev0.ListDependenciesResponse, error) {
	resp, err := s.handleListDependencies(ctx, nil)
	if err != nil {
		return nil, err
	}
	return resp.GetListDependencies(), nil
}

// --- Handlers ---

func (s *PythonCodeServer) handleListSymbols(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	r := req.GetListSymbols()
	file := ""
	if r != nil {
		file = r.File
	}
	symbols, err := s.symbols.ListSymbols(ctx, file)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_ListSymbols{ListSymbols: &codev0.ListSymbolsResponse{
			Status: &codev0.ListSymbolsStatus{State: codev0.ListSymbolsStatus_ERROR, Message: err.Error()},
		}}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_ListSymbols{ListSymbols: &codev0.ListSymbolsResponse{
		Status:  &codev0.ListSymbolsStatus{State: codev0.ListSymbolsStatus_SUCCESS},
		Symbols: symbols,
	}}}, nil
}

func (s *PythonCodeServer) handleGetProjectInfo(_ context.Context, _ *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	srcDir := s.SourceDir
	resp := &codev0.GetProjectInfoResponse{Language: "python"}

	// Parse pyproject.toml
	data, err := s.FS.ReadFile(filepath.Join(srcDir, "pyproject.toml"))
	if err == nil {
		resp.Module, resp.LanguageVersion = parsePyprojectTOML(string(data))
	}

	// Discover packages (directories with __init__.py)
	resp.Packages = s.discoverPackages(srcDir)

	// File hashes
	resp.FileHashes = s.computeFileHashes(srcDir)

	// Dependencies from uv pip list
	resp.Dependencies = s.listUVDependencies(srcDir)

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
		packages = append(packages, rootPkg)
	}

	return packages
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
