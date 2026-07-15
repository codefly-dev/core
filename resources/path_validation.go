package resources

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// validateResourcePathComponent validates a logical resource name before it is
// used as one directory component. Job and library names are not backed by the
// protobuf hostname validators used by services/workspaces, so this is their
// model-boundary confinement check.
func validateResourcePathComponent(kind, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s name cannot be empty", kind)
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("%s name %q must be a single path component", kind, name)
	}
	return nil
}

// validateResourceRelativePath accepts nested relative paths but rejects
// absolute/traversing and cross-platform backslash forms.
func validateResourceRelativePath(kind, p string) error {
	if !filepath.IsLocal(p) || strings.ContainsAny(p, "\x00\\") {
		return fmt.Errorf("%s path %q must stay within the resource directory", kind, p)
	}
	return nil
}

// validateResourcePathOverride keeps relative overrides inside their owning
// resource. Absolute overrides remain supported because they are an explicit
// monorepo feature; callers choosing one are deliberately selecting an
// external location.
func validateResourcePathOverride(kind string, override *string) error {
	if override == nil {
		return nil
	}
	if strings.ContainsRune(*override, '\x00') {
		return fmt.Errorf("%s path override contains NUL", kind)
	}
	if filepath.IsAbs(*override) {
		return nil
	}
	return validateResourceRelativePath(kind+" override", *override)
}

func validateModuleReferencePath(ref *ModuleReference) error {
	if ref == nil {
		return fmt.Errorf("module reference cannot be nil")
	}
	if err := validateResourcePathComponent("module", ref.Name); err != nil {
		return err
	}
	return validateResourcePathOverride("module", ref.PathOverride)
}

func validateServiceReferencePath(ref *ServiceReference) error {
	if ref == nil {
		return fmt.Errorf("service reference cannot be nil")
	}
	if err := validateResourcePathComponent("service", ref.Name); err != nil {
		return err
	}
	return validateResourcePathOverride("service", ref.PathOverride)
}

func validateApplicationReferencePath(ref *ApplicationReference) error {
	if ref == nil {
		return fmt.Errorf("application reference cannot be nil")
	}
	return validateResourcePathComponent("application", ref.Name)
}

func validateJobReferencePath(ref *JobReference) error {
	if ref == nil {
		return fmt.Errorf("job reference cannot be nil")
	}
	if err := validateResourcePathComponent("job", ref.Name); err != nil {
		return err
	}
	return validateResourcePathOverride("job", ref.PathOverride)
}

func (workspace *Workspace) validatePaths() error {
	if err := validateResourcePathComponent("workspace", workspace.Name); err != nil {
		return err
	}
	if workspace.Path != "" {
		if err := validateResourceRelativePath("workspace", workspace.Path); err != nil {
			return err
		}
	}
	for _, ref := range workspace.Modules {
		if err := validateModuleReferencePath(ref); err != nil {
			return err
		}
	}
	for _, ref := range workspace.Services {
		if err := validateServiceReferencePath(ref); err != nil {
			return err
		}
	}
	for _, ref := range workspace.Jobs {
		if err := validateJobReferencePath(ref); err != nil {
			return err
		}
	}
	return nil
}

func (mod *Module) validatePaths() error {
	if err := validateResourcePathComponent("module", mod.Name); err != nil {
		return err
	}
	if err := validateResourcePathOverride("module", mod.PathOverride); err != nil {
		return err
	}
	for _, ref := range mod.ServiceReferences {
		if err := validateServiceReferencePath(ref); err != nil {
			return err
		}
	}
	for _, ref := range mod.JobReferences {
		if err := validateJobReferencePath(ref); err != nil {
			return err
		}
	}
	for _, ref := range mod.ApplicationReferences {
		if err := validateApplicationReferencePath(ref); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) validatePaths() error {
	if err := validateResourcePathComponent("service", service.Name); err != nil {
		return err
	}
	return validateResourcePathOverride("service", service.PathOverride)
}

func (app *Application) validatePaths() error {
	if err := validateResourcePathComponent("application", app.Name); err != nil {
		return err
	}
	return validateResourcePathOverride("application", app.PathOverride)
}

// validateServiceUniquePath validates the intentional module/service pair
// before it is used as two filesystem components by route persistence.
func validateServiceUniquePath(unique string) error {
	ref, err := ParseServiceWithOptionalModule(unique)
	if err != nil || ref.Module == "" || ref.Name == "" {
		return fmt.Errorf("service identity %q must be module/service", unique)
	}
	if err := validateResourcePathComponent("module", ref.Module); err != nil {
		return err
	}
	return validateResourcePathComponent("service", ref.Name)
}

// validateResolvedPathWithin checks the real paths after directory creation so
// an existing in-tree symlink cannot redirect a route write outside its root.
func validateResolvedPathWithin(root, target string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("cannot resolve route root %q: %w", root, err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return fmt.Errorf("cannot resolve route directory %q: %w", target, err)
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil {
		return fmt.Errorf("cannot compare route directory to root: %w", err)
	}
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("route directory %q resolves outside root %q", target, root)
	}
	info, err := os.Stat(resolvedTarget)
	if err != nil {
		return fmt.Errorf("cannot stat route directory %q: %w", target, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("route path %q is not a directory", target)
	}
	return nil
}
