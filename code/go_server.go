package code

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// GoCodeServer extends DefaultCodeServer with Go-specific operations:
// ListSymbols (via pluggable SymbolProvider), GetProjectInfo (go list),
// and ListDependencies (go list -m). It provides the same data as the
// go-generic agent but without requiring Docker or gopls.
type GoCodeServer struct {
	*DefaultCodeServer
	symbols SymbolProvider
}

// GoServerOption configures a GoCodeServer.
type GoServerOption func(*GoCodeServer)

// WithSymbolProvider overrides the default AST-based symbol provider.
// Use this to plug in an LSP-backed provider when gopls is available.
func WithSymbolProvider(sp SymbolProvider) GoServerOption {
	return func(s *GoCodeServer) {
		s.symbols = sp
	}
}

// NewGoCodeServer creates a Go-aware code server. By default it uses
// ASTSymbolProvider (ParseGoTree) for symbols. Pass WithSymbolProvider()
// to use LSP or any other implementation. ServerOptions are forwarded to
// the embedded DefaultCodeServer (e.g. WithVFS).
func NewGoCodeServer(dir string, serverOpts []ServerOption, goOpts ...GoServerOption) *GoCodeServer {
	base := NewDefaultCodeServer(dir, serverOpts...)
	s := &GoCodeServer{
		DefaultCodeServer: base,
		symbols:           NewASTSymbolProviderVFS(dir, base.FS),
	}
	for _, o := range goOpts {
		o(s)
	}
	s.registerGoOverrides()
	return s
}

// VFS returns the underlying VFS of the code server.
func (s *GoCodeServer) VFS() VFS { return s.FS }

// GetProjectInfo implements the standalone gRPC RPC (not through Execute).
func (s *GoCodeServer) GetProjectInfo(ctx context.Context, req *codev0.GetProjectInfoRequest) (*codev0.GetProjectInfoResponse, error) {
	resp, err := s.handleGetProjectInfo(ctx, nil)
	if err != nil {
		return nil, err
	}
	return resp.GetGetProjectInfo(), nil
}

// ListSymbols implements the standalone gRPC RPC (not through Execute).
func (s *GoCodeServer) ListSymbols(ctx context.Context, req *codev0.ListSymbolsRequest) (*codev0.ListSymbolsResponse, error) {
	codeReq := &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ListSymbols{ListSymbols: req},
	}
	resp, err := s.handleListSymbols(ctx, codeReq)
	if err != nil {
		return nil, err
	}
	return resp.GetListSymbols(), nil
}

// ListDependencies implements the standalone gRPC RPC.
func (s *GoCodeServer) ListDependencies(ctx context.Context, req *codev0.ListDependenciesRequest) (*codev0.ListDependenciesResponse, error) {
	resp, err := s.handleListDependencies(ctx, nil)
	if err != nil {
		return nil, err
	}
	return resp.GetListDependencies(), nil
}

func (s *GoCodeServer) registerGoOverrides() {
	s.Override("list_symbols", s.handleListSymbols)
	s.Override("get_project_info", s.handleGetProjectInfo)
	s.Override("list_dependencies", s.handleListDependencies)
}

// --- ListSymbols (delegates to SymbolProvider) ---

func (s *GoCodeServer) handleListSymbols(ctx context.Context, req *codev0.CodeRequest) (*codev0.CodeResponse, error) {
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

// --- GetProjectInfo (go.mod + go list) ---

func (s *GoCodeServer) handleGetProjectInfo(ctx context.Context, _ *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	srcDir := s.SourceDir
	resp := &codev0.GetProjectInfoResponse{Language: "go"}

	modData, err := s.FS.ReadFile(filepath.Join(srcDir, "go.mod"))
	if err != nil {
		resp.Error = fmt.Sprintf("read go.mod: %v", err)
		return wrapProjectInfo(resp), nil
	}
	for _, line := range strings.Split(string(modData), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			resp.Module = strings.TrimPrefix(line, "module ")
		}
		if strings.HasPrefix(line, "go ") {
			resp.LanguageVersion = strings.TrimPrefix(line, "go ")
		}
	}

	if pkgs := goListPackages(ctx, srcDir); pkgs != nil {
		resp.Packages = pkgs
	}

	if deps, err := goListDependencies(ctx, srcDir); err == nil {
		resp.Dependencies = deps
	}

	resp.FileHashes = computeFileHashes(s.FS, srcDir)

	return wrapProjectInfo(resp), nil
}

// --- ListDependencies (go list -m) ---

func (s *GoCodeServer) handleListDependencies(ctx context.Context, _ *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	deps, err := goListDependencies(ctx, s.SourceDir)
	if err != nil {
		return &codev0.CodeResponse{Result: &codev0.CodeResponse_ListDependencies{
			ListDependencies: &codev0.ListDependenciesResponse{Error: fmt.Sprintf("go list -m failed: %v", err)},
		}}, nil
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_ListDependencies{
		ListDependencies: &codev0.ListDependenciesResponse{Dependencies: deps},
	}}, nil
}

// --- Shared helpers (extracted from go-generic agent) ---

type goPackageJSON struct {
	Dir        string   `json:"Dir"`
	ImportPath string   `json:"ImportPath"`
	GoFiles    []string `json:"GoFiles"`
	Imports    []string `json:"Imports"`
	Doc        string   `json:"Doc"`
}

type goModJSON struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Main    bool   `json:"Main"`
	Dir     string `json:"Dir"`
}

func goListPackages(ctx context.Context, dir string) []*codev0.PackageInfo {
	cmd := exec.CommandContext(ctx, "go", "list", "-json", "./...")
	cmd.Dir = dir
	// GOWORK=off because `dir` may be a third-party repo nested under
	// a workspace that doesn't include it (e.g. testdata/repos/* in
	// the codefly monorepo, or any consumer of this package that
	// invokes it from inside their own go.work). Without this, `go
	// list` errors with "directory prefix . does not contain modules
	// listed in go.work" and the test gets zero packages.
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var pkgs []*codev0.PackageInfo
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var pkg goPackageJSON
		if err := decoder.Decode(&pkg); err != nil {
			break
		}
		relPath, _ := filepath.Rel(dir, pkg.Dir)
		if relPath == "" {
			relPath = "."
		}
		pkgs = append(pkgs, &codev0.PackageInfo{
			Name: pkg.ImportPath, RelativePath: relPath,
			Files: pkg.GoFiles, Imports: pkg.Imports, Doc: pkg.Doc,
		})
	}
	return pkgs
}

func goListDependencies(ctx context.Context, dir string) ([]*codev0.Dependency, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json", "all")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off") // see goListPackages
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	// Empty (non-nil) slice on success: a module with zero require lines
	// is a valid result, not an error. The earlier `var deps []*Dep` form
	// returned nil in that case, which the handler couldn't distinguish
	// from a failed exec.
	deps := []*codev0.Dependency{}
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var m goModJSON
		if err := decoder.Decode(&m); err != nil {
			break
		}
		if m.Main {
			continue
		}
		deps = append(deps, &codev0.Dependency{
			Name: m.Path, Version: m.Version, Direct: m.Dir != "",
		})
	}
	return deps, nil
}

// goFileExtensions are the file extensions relevant for Go project hashing.
var goFileExtensions = map[string]bool{
	".go":  true,
	".mod": true,
	".sum": true,
}

func computeFileHashes(vfs VFS, dir string) map[string]string {
	return ComputeFileHashes(vfs, dir, goFileExtensions)
}

func wrapProjectInfo(resp *codev0.GetProjectInfoResponse) *codev0.CodeResponse {
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetProjectInfo{GetProjectInfo: resp}}
}

