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

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// GoCodeServer extends DefaultCodeServer with Go-specific project metadata
// and dependency operations.
type GoCodeServer struct {
	*DefaultCodeServer
}

// GoServerOption configures a GoCodeServer.
type GoServerOption func(*GoCodeServer)

// NewGoCodeServer creates a Go-aware code server.
// ServerOptions are forwarded to the embedded DefaultCodeServer (e.g. WithVFS).
func NewGoCodeServer(dir string, serverOpts []ServerOption, goOpts ...GoServerOption) *GoCodeServer {
	base := NewDefaultCodeServer(dir, serverOpts...)
	s := &GoCodeServer{
		DefaultCodeServer: base,
	}
	for _, o := range goOpts {
		o(s)
	}
	s.registerGoOverrides()
	return s
}

// VFS returns the underlying VFS of the code server.
func (s *GoCodeServer) VFS() VFS { return s.FS }

func (s *GoCodeServer) registerGoOverrides() {
	s.Override("get_project_info", s.handleGetProjectInfo)
	s.Override("list_dependencies", s.handleListDependencies)
}

// --- GetProjectInfo (go.mod + go list) ---

func (s *GoCodeServer) handleGetProjectInfo(ctx context.Context, _ *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	srcDir := s.SourceDir
	resp := &codev0.GetProjectInfoResponse{Language: "go"}

	modData, err := s.FS.ReadFile(filepath.Join(srcDir, "go.mod"))
	if err != nil {
		return codeFailure(wrapProjectInfo(resp), basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.get-project-info", fmt.Sprintf("read go.mod: %v", err)), nil
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
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_ListDependencies{
			ListDependencies: &codev0.ListDependenciesResponse{},
		}}, basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED, "code.list-dependencies", fmt.Sprintf("go list -m failed: %v", err)), nil
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
	// Try with the caller's environment first — projects that
	// rely on a go.work wiring local modules (Mind, multi-module
	// monorepos) need it. If go.work blocks the listing because
	// `dir` is a third-party repo nested under a parent go.work
	// that doesn't include it (testdata fixtures, vendored repos),
	// retry with GOWORK=off so the local go.mod takes over.
	out, err := runGoList(ctx, dir, false)
	if err != nil {
		out, err = runGoList(ctx, dir, true)
		if err != nil {
			return nil
		}
	}
	// Resolve symlinks on BOTH `dir` and each `pkg.Dir` before
	// computing relative paths. On macOS /var → /private/var (and
	// the reverse for some test temp dirs), so the directions may
	// not match. Resolving both sides ensures filepath.Rel
	// produces a clean relative path instead of
	// "../../../../private/var/folders/.../proj" or its inverse.
	// EvalSymlinks failures fall back to the raw path — better
	// imperfect than empty.
	resolvedDir := dir
	if rd, err := filepath.EvalSymlinks(dir); err == nil {
		resolvedDir = rd
	}
	var pkgs []*codev0.PackageInfo
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var pkg goPackageJSON
		if err := decoder.Decode(&pkg); err != nil {
			break
		}
		resolvedPkgDir := pkg.Dir
		if rpd, err := filepath.EvalSymlinks(pkg.Dir); err == nil {
			resolvedPkgDir = rpd
		}
		relPath, _ := filepath.Rel(resolvedDir, resolvedPkgDir)
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

// runGoList shells `go list -json ./...` in `dir`. workOff toggles
// GOWORK=off — see goListPackages for the workspace-vs-standalone
// retry rationale.
func runGoList(ctx context.Context, dir string, workOff bool) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-json", "./...")
	cmd.Dir = dir
	if workOff {
		cmd.Env = append(os.Environ(), "GOWORK=off")
	}
	return cmd.Output()
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
