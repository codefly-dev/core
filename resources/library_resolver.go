package resources

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/wool"
)

// LibraryResolver handles library dependency resolution and setup
type LibraryResolver struct {
	workspace *Workspace
}

// NewLibraryResolver creates a new library resolver for a workspace
func NewLibraryResolver(workspace *Workspace) *LibraryResolver {
	return &LibraryResolver{workspace: workspace}
}

// ResolveVersion finds the best matching version for a constraint
func (r *LibraryResolver) ResolveVersion(ctx context.Context, name, constraint string) (*Library, string, error) {
	w := wool.Get(ctx).In("LibraryResolver.ResolveVersion")

	lib, err := r.workspace.LoadLibraryFromName(ctx, name)
	if err != nil {
		return nil, "", w.Wrapf(err, "failed to load library %s", name)
	}

	// If no constraint, return current version
	if constraint == "" {
		return lib, lib.Version, nil
	}

	// Parse constraint
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return nil, "", w.Wrapf(err, "invalid version constraint: %s", constraint)
	}

	// Get available versions (from git tags or just current version)
	versions, err := r.getAvailableVersions(ctx, lib)
	if err != nil {
		// If we can't get versions, just check current
		v, verr := semver.NewVersion(lib.Version)
		if verr != nil {
			return nil, "", w.Wrapf(verr, "invalid library version: %s", lib.Version)
		}
		if c.Check(v) {
			return lib, lib.Version, nil
		}
		return nil, "", w.NewError("library %s version %s does not satisfy %s", name, lib.Version, constraint)
	}

	// Find best matching version (highest that satisfies constraint)
	var best *semver.Version
	for _, v := range versions {
		if c.Check(v) {
			if best == nil || v.GreaterThan(best) {
				best = v
			}
		}
	}

	if best == nil {
		return nil, "", w.NewError("no version of %s satisfies constraint %s", name, constraint)
	}

	return lib, best.String(), nil
}

// getAvailableVersions returns available versions from git tags
func (r *LibraryResolver) getAvailableVersions(ctx context.Context, lib *Library) ([]*semver.Version, error) {
	w := wool.Get(ctx).In("LibraryResolver.getAvailableVersions")

	if !lib.IsGitSubmodule(ctx) {
		// Local only - return current version
		v, err := semver.NewVersion(lib.Version)
		if err != nil {
			return nil, err
		}
		return []*semver.Version{v}, nil
	}

	// Get git tags
	cmd := exec.CommandContext(ctx, "git", "tag", "-l", "v*")
	cmd.Dir = lib.Dir()
	out, err := cmd.Output()
	if err != nil {
		return nil, w.Wrapf(err, "failed to get git tags")
	}

	var versions []*semver.Version
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Remove 'v' prefix
		verStr := strings.TrimPrefix(line, "v")
		v, err := semver.NewVersion(verStr)
		if err != nil {
			continue // Skip invalid versions
		}
		versions = append(versions, v)
	}

	return versions, nil
}

// SetupLocalDevelopment configures a service for local library development
func (r *LibraryResolver) SetupLocalDevelopment(ctx context.Context, svc *Service) error {
	w := wool.Get(ctx).In("LibraryResolver.SetupLocalDevelopment")

	for _, dep := range svc.LibraryDependencies {
		lib, _, err := r.ResolveVersion(ctx, dep.Name, dep.Version)
		if err != nil {
			return w.Wrapf(err, "failed to resolve library %s", dep.Name)
		}

		for _, lang := range dep.Languages {
			switch lang {
			case "go":
				if err := r.setupGoReplace(ctx, svc, lib); err != nil {
					return w.Wrapf(err, "failed to setup Go replace for %s", lib.Name)
				}
			case "python":
				if err := r.setupPythonEditable(ctx, svc, lib); err != nil {
					return w.Wrapf(err, "failed to setup Python editable for %s", lib.Name)
				}
			case "typescript", "javascript":
				if err := r.setupNpmLink(ctx, svc, lib); err != nil {
					return w.Wrapf(err, "failed to setup npm link for %s", lib.Name)
				}
			default:
				w.Warn("unsupported language for library setup", wool.Field("language", lang))
			}
		}
	}

	return nil
}

// setupGoReplace adds a replace directive to the service's go.mod
func (r *LibraryResolver) setupGoReplace(ctx context.Context, svc *Service, lib *Library) error {
	w := wool.Get(ctx).In("LibraryResolver.setupGoReplace")

	goLang := lib.GetLanguage("go")
	if goLang == nil {
		return w.NewError("library %s has no Go export", lib.Name)
	}

	// Get the Go module path from library exports
	if len(goLang.Exports) == 0 {
		return w.NewError("library %s Go export has no module path defined", lib.Name)
	}
	modulePath := goLang.Exports[0]

	// Calculate relative path from service to library
	libPath := lib.LanguagePath(goLang)
	relPath, err := filepath.Rel(svc.Dir(), libPath)
	if err != nil {
		return w.Wrapf(err, "failed to calculate relative path")
	}

	// Use go mod edit to add replace directive
	cmd := exec.CommandContext(ctx, "go", "mod", "edit",
		"-replace", fmt.Sprintf("%s=%s", modulePath, relPath))
	cmd.Dir = svc.Dir()
	if out, err := cmd.CombinedOutput(); err != nil {
		return w.Wrapf(err, "go mod edit failed: %s", string(out))
	}

	w.Debug("added Go replace directive",
		wool.Field("module", modulePath),
		wool.Field("path", relPath))

	return nil
}

// removeGoReplace removes a replace directive from the service's go.mod
func (r *LibraryResolver) removeGoReplace(ctx context.Context, svc *Service, lib *Library) error {
	w := wool.Get(ctx).In("LibraryResolver.removeGoReplace")

	goLang := lib.GetLanguage("go")
	if goLang == nil {
		return nil // No Go export, nothing to remove
	}

	if len(goLang.Exports) == 0 {
		return nil
	}
	modulePath := goLang.Exports[0]

	// Use go mod edit to drop replace directive
	cmd := exec.CommandContext(ctx, "go", "mod", "edit",
		"-dropreplace", modulePath)
	cmd.Dir = svc.Dir()
	if out, err := cmd.CombinedOutput(); err != nil {
		return w.Wrapf(err, "go mod edit failed: %s", string(out))
	}

	return nil
}

// setupPythonEditable sets up an editable install for Python
func (r *LibraryResolver) setupPythonEditable(ctx context.Context, svc *Service, lib *Library) error {
	w := wool.Get(ctx).In("LibraryResolver.setupPythonEditable")

	pyLang := lib.GetLanguage("python")
	if pyLang == nil {
		return w.NewError("library %s has no Python export", lib.Name)
	}

	libPath := lib.LanguagePath(pyLang)

	// Use pip install -e for editable install
	cmd := exec.CommandContext(ctx, "pip", "install", "-e", libPath)
	cmd.Dir = svc.Dir()
	if out, err := cmd.CombinedOutput(); err != nil {
		return w.Wrapf(err, "pip install -e failed: %s", string(out))
	}

	w.Debug("installed Python library in editable mode", wool.Field("path", libPath))

	return nil
}

// setupNpmLink sets up npm link for TypeScript/JavaScript
func (r *LibraryResolver) setupNpmLink(ctx context.Context, svc *Service, lib *Library) error {
	w := wool.Get(ctx).In("LibraryResolver.setupNpmLink")

	// Check for typescript or javascript export
	lang := lib.GetLanguage("typescript")
	if lang == nil {
		lang = lib.GetLanguage("javascript")
	}
	if lang == nil {
		return w.NewError("library %s has no TypeScript/JavaScript export", lib.Name)
	}

	libPath := lib.LanguagePath(lang)

	// First: npm link in the library
	cmd := exec.CommandContext(ctx, "npm", "link")
	cmd.Dir = libPath
	if out, err := cmd.CombinedOutput(); err != nil {
		return w.Wrapf(err, "npm link in library failed: %s", string(out))
	}

	// Get package name from exports
	if len(lang.Exports) == 0 {
		return w.NewError("library %s TypeScript export has no package name defined", lib.Name)
	}
	packageName := lang.Exports[0]

	// Then: npm link <package> in the service
	cmd = exec.CommandContext(ctx, "npm", "link", packageName)
	cmd.Dir = svc.Dir()
	if out, err := cmd.CombinedOutput(); err != nil {
		return w.Wrapf(err, "npm link in service failed: %s", string(out))
	}

	w.Debug("linked npm package", wool.Field("package", packageName))

	return nil
}

// CleanupLocalDevelopment removes local development setup (for production builds)
func (r *LibraryResolver) CleanupLocalDevelopment(ctx context.Context, svc *Service) error {
	w := wool.Get(ctx).In("LibraryResolver.CleanupLocalDevelopment")

	for _, dep := range svc.LibraryDependencies {
		lib, err := r.workspace.LoadLibraryFromName(ctx, dep.Name)
		if err != nil {
			w.Warn("failed to load library for cleanup", wool.Field("name", dep.Name))
			continue
		}

		for _, lang := range dep.Languages {
			switch lang {
			case "go":
				if err := r.removeGoReplace(ctx, svc, lib); err != nil {
					w.Warn("failed to remove Go replace", wool.ErrField(err))
				}
			}
			// Python and npm don't need cleanup for production builds
		}
	}

	return nil
}

// GetLibraryMounts returns mount specifications for Docker builds
func (r *LibraryResolver) GetLibraryMounts(ctx context.Context, svc *Service) ([]LibraryMount, error) {
	var mounts []LibraryMount

	for _, dep := range svc.LibraryDependencies {
		lib, err := r.workspace.LoadLibraryFromName(ctx, dep.Name)
		if err != nil {
			return nil, err
		}

		for _, lang := range dep.Languages {
			langExport := lib.GetLanguage(lang)
			if langExport == nil {
				continue
			}

			mounts = append(mounts, LibraryMount{
				LibraryName: lib.Name,
				Language:    lang,
				SourcePath:  lib.LanguagePath(langExport),
				TargetPath:  fmt.Sprintf("/libraries/%s/%s", lib.Name, lang),
				ModulePath:  langExport.Exports,
			})
		}
	}

	return mounts, nil
}

// LibraryMount represents a mount specification for Docker
type LibraryMount struct {
	LibraryName string
	Language    string
	SourcePath  string   // Absolute path on host
	TargetPath  string   // Path in container
	ModulePath  []string // Package/module paths (for Go replace, etc.)
}
