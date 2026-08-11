package code

// ARCHITECTURE: Rust project knowledge comes from Cargo's typed metadata API,
// not from orchestration code or an ad-hoc Cargo.toml parser. The Codefly Rust
// agent embeds this server so local and remote Tooling calls share one project
// inspection contract while the project remains inside the agent boundary.

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"github.com/codefly-dev/core/internal/cargometadata"
)

// RustCodeServer extends DefaultCodeServer with Cargo-owned project metadata.
type RustCodeServer struct {
	*DefaultCodeServer
}

// NewRustCodeServer creates a Rust-aware Code server.
func NewRustCodeServer(dir string, serverOpts ...ServerOption) *RustCodeServer {
	server := &RustCodeServer{DefaultCodeServer: NewDefaultCodeServer(dir, serverOpts...)}
	server.Override("get_project_info", server.handleGetProjectInfo)
	return server
}

func (s *RustCodeServer) handleGetProjectInfo(ctx context.Context, _ *codev0.CodeRequest) (*codev0.CodeResponse, error) {
	sourceRoot := s.SourceDir
	response := &codev0.GetProjectInfoResponse{Language: "rust"}
	metadata, err := cargometadata.Run(ctx, sourceRoot, cargometadata.Options{
		ManifestPath: filepath.Join(sourceRoot, "Cargo.toml"),
		Locked:       true, NoDependencies: true, Offline: true,
	})
	if err != nil {
		return codeFailure(wrapRustProjectInfo(response), basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED,
			"code.get-project-info", err.Error()), nil
	}

	packages := rustPackagesWithinRoot(sourceRoot, metadata.Packages)
	if len(packages) == 0 {
		return codeFailure(wrapRustProjectInfo(response), basev0.FailureCode_FAILURE_CODE_VALIDATION_FAILED,
			"code.get-project-info", "cargo metadata returned no package within the code-unit root"), nil
	}

	editions := make(map[string]struct{})
	dependencies := make(map[string]*codev0.Dependency)
	for _, pkg := range packages {
		packageRoot := filepath.Dir(pkg.ManifestPath)
		relativePackage, _ := relativePathWithin(sourceRoot, packageRoot)
		packageInfo := &codev0.PackageInfo{Name: pkg.Name, RelativePath: relativePackage}
		if pkg.Edition != "" {
			editions[pkg.Edition] = struct{}{}
		}
		for _, target := range pkg.Targets {
			relativeTarget, inside := relativePathWithin(packageRoot, target.SrcPath)
			if inside {
				packageInfo.Files = append(packageInfo.Files, relativeTarget)
			}
		}
		packageInfo.Files = sortedUniqueStrings(packageInfo.Files)
		for _, dependency := range pkg.Dependencies {
			importName := dependency.Name
			if dependency.Rename != nil && *dependency.Rename != "" {
				importName = *dependency.Rename
			}
			packageInfo.Imports = append(packageInfo.Imports, importName)
			key := dependency.Name + "\x00" + dependency.Req
			dependencies[key] = &codev0.Dependency{Name: dependency.Name, Version: dependency.Req, Direct: true}
		}
		packageInfo.Imports = sortedUniqueStrings(packageInfo.Imports)
		response.Packages = append(response.Packages, packageInfo)
	}
	response.Module = rustModuleName(sourceRoot, response.Packages)
	response.LanguageVersion = strings.Join(sortedStringSet(editions), ",")
	for _, dependency := range dependencies {
		response.Dependencies = append(response.Dependencies, dependency)
	}
	sort.Slice(response.Dependencies, func(i, j int) bool {
		if response.Dependencies[i].Name != response.Dependencies[j].Name {
			return response.Dependencies[i].Name < response.Dependencies[j].Name
		}
		return response.Dependencies[i].Version < response.Dependencies[j].Version
	})

	response.FileHashes = ComputeFileHashes(s.FS, sourceRoot, rustProjectFileExtensions)
	response.SourceFiles, err = inspectSourceImports(ctx, s.FS, sourceRoot, "rust")
	if err != nil {
		return codeFailure(wrapRustProjectInfo(response), basev0.FailureCode_FAILURE_CODE_VALIDATION_FAILED,
			"code.get-project-info", err.Error()), nil
	}
	return wrapRustProjectInfo(response), nil
}

func rustPackagesWithinRoot(root string, packages []cargometadata.Package) []cargometadata.Package {
	selected := make([]cargometadata.Package, 0, len(packages))
	for _, pkg := range packages {
		if _, inside := relativePathWithin(root, filepath.Dir(pkg.ManifestPath)); inside {
			selected = append(selected, pkg)
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		left, _ := relativePathWithin(root, filepath.Dir(selected[i].ManifestPath))
		right, _ := relativePathWithin(root, filepath.Dir(selected[j].ManifestPath))
		if left != right {
			return left < right
		}
		return selected[i].Name < selected[j].Name
	})
	return selected
}

// relativePathWithin canonicalizes both sides before enforcing the code-unit
// boundary. This handles macOS /var -> /private/var aliases without ever
// publishing a Cargo path that escapes the requested root.
func relativePathWithin(root, candidate string) (string, bool) {
	canonicalRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	if resolved, resolveErr := filepath.EvalSymlinks(canonicalRoot); resolveErr == nil {
		canonicalRoot = resolved
	}
	canonicalCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", false
	}
	if resolved, resolveErr := filepath.EvalSymlinks(canonicalCandidate); resolveErr == nil {
		canonicalCandidate = resolved
	}
	relative, err := filepath.Rel(canonicalRoot, canonicalCandidate)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	if relative == "" {
		relative = "."
	}
	return filepath.ToSlash(relative), true
}

func rustModuleName(sourceRoot string, packages []*codev0.PackageInfo) string {
	for _, pkg := range packages {
		if pkg.RelativePath == "." {
			return pkg.Name
		}
	}
	return filepath.Base(filepath.Clean(sourceRoot))
}

func sortedUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	return sortedStringSet(seen)
}

func sortedStringSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

var rustProjectFileExtensions = map[string]bool{
	".rs": true, ".toml": true, ".lock": true,
}

func wrapRustProjectInfo(response *codev0.GetProjectInfoResponse) *codev0.CodeResponse {
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetProjectInfo{GetProjectInfo: response}}
}
