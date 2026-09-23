package resources

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// WorkspaceReference selects a workspace owned and versioned by another repository.
// Path is an explicit local development reference. Source and Version select a
// published revision; acquiring it is the host's responsibility, never Core's.
type WorkspaceReference struct {
	Name      string `yaml:"name"`
	Source    string `yaml:"source,omitempty"`
	Version   string `yaml:"version,omitempty"`
	Workspace string `yaml:"workspace,omitempty"`
	Path      string `yaml:"path,omitempty"`
}

func (ref *WorkspaceReference) validate() error {
	if ref == nil {
		return fmt.Errorf("workspace reference cannot be nil")
	}
	if err := validateResourcePathComponent("composed workspace", ref.Name); err != nil {
		return err
	}
	if ref.Path != "" {
		if ref.Source != "" || ref.Version != "" || ref.Workspace != "" {
			return fmt.Errorf("workspace %q must select either path or source/version", ref.Name)
		}
		return validateModuleReferencePathOverride(&ref.Path)
	}
	parts := strings.Split(ref.Source, "/")
	if len(parts) != 2 {
		return fmt.Errorf("workspace %q source must be owner/repository", ref.Name)
	}
	for _, part := range parts {
		if err := validateResourcePathComponent("workspace source", part); err != nil {
			return err
		}
		if strings.ContainsAny(part, " :@?#%") {
			return fmt.Errorf("invalid workspace source %q", ref.Source)
		}
	}
	if strings.TrimSpace(ref.Version) == "" {
		return fmt.Errorf("workspace %q requires a version", ref.Name)
	}
	if ref.Workspace != "" {
		return validateResourceRelativePath("workspace subpath", ref.Workspace)
	}
	return nil
}

// WorkspaceResolver is the acquisition boundary supplied by a CLI or other host.
// It returns the selected workspace directory after verifying the requested release.
type WorkspaceResolver func(context.Context, *WorkspaceReference) (string, error)

type workspaceResolverKey struct{}
type workspaceStackKey struct{}

// WithWorkspaceResolver attaches a resolver to one load without process-global state.
func WithWorkspaceResolver(ctx context.Context, resolver WorkspaceResolver) context.Context {
	return context.WithValue(ctx, workspaceResolverKey{}, resolver)
}

func enterWorkspaceComposition(ctx context.Context, dir string) (context.Context, error) {
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	stack, _ := ctx.Value(workspaceStackKey{}).([]string)
	if slices.Contains(stack, canonical) {
		return nil, fmt.Errorf("workspace composition cycle at %s", canonical)
	}
	if len(stack) >= 32 {
		return nil, fmt.Errorf("workspace composition exceeds 32 levels")
	}
	next := append(append([]string(nil), stack...), canonical)
	return context.WithValue(ctx, workspaceStackKey{}, next), nil
}

func (workspace *Workspace) composeWorkspaces(ctx context.Context) error {
	if len(workspace.Workspaces) == 0 && len(workspace.Solutions) == 0 {
		return nil
	}
	workspace.derivedModules = make(map[string]bool)
	workspace.moduleDeclarationDirs = make(map[string]string)
	seen := make(map[string]bool)
	for _, ref := range workspace.Modules {
		if seen[ref.Name] {
			return fmt.Errorf("duplicate module %q", ref.Name)
		}
		seen[ref.Name] = true
	}
	add := func(ref *ModuleReference, owner *Workspace) error {
		if seen[ref.Name] {
			return fmt.Errorf("composed module %q conflicts with another declaration; change its owner rather than repinning it", ref.Name)
		}
		seen[ref.Name] = true
		copy := *ref
		if owner != workspace && (ref.PathOverride != nil || ref.Source == "") {
			resolved := owner.ModulePath(ctx, ref)
			copy.PathOverride = &resolved
		}
		workspace.Modules = append(workspace.Modules, &copy)
		workspace.derivedModules[ref.Name] = true
		workspace.moduleDeclarationDirs[ref.Name] = owner.ModuleDeclarationDir(ref.Name)
		return nil
	}
	workspaceNames := make(map[string]bool)
	for _, ref := range workspace.Workspaces {
		if workspaceNames[ref.Name] {
			return fmt.Errorf("duplicate workspace %q", ref.Name)
		}
		workspaceNames[ref.Name] = true
		dir := ref.Path
		if dir != "" {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(workspace.Dir(), dir)
			}
		} else {
			resolve, _ := ctx.Value(workspaceResolverKey{}).(WorkspaceResolver)
			if resolve == nil {
				return fmt.Errorf("workspace %q at %s@%s requires host resolution", ref.Name, ref.Source, ref.Version)
			}
			var err error
			dir, err = resolve(ctx, ref)
			if err != nil {
				return fmt.Errorf("resolve workspace %q: %w", ref.Name, err)
			}
		}
		child, err := LoadWorkspaceFromDir(ctx, dir)
		if err != nil {
			return fmt.Errorf("compose workspace %q: %w", ref.Name, err)
		}
		if child.Name != ref.Name {
			return fmt.Errorf("workspace %q resolves to workspace %q", ref.Name, child.Name)
		}
		if child.Layout == LayoutKindFlat {
			return fmt.Errorf("composed workspace %q must have modules layout", ref.Name)
		}
		workspace.composedWorkspaces = append(workspace.composedWorkspaces, child)
		for _, mod := range child.Modules {
			if err := add(mod, child); err != nil {
				return err
			}
		}
	}
	for _, solution := range workspace.Solutions {
		if err := add(solution, workspace); err != nil {
			return err
		}
	}
	return nil
}

func (workspace *Workspace) authoredModules() []*ModuleReference {
	var refs []*ModuleReference
	for _, ref := range workspace.Modules {
		if !workspace.derivedModules[ref.Name] {
			refs = append(refs, ref)
		}
	}
	return refs
}

// ModuleDeclarationDir identifies the owner of a module's resolution/trust policy.
func (workspace *Workspace) ModuleDeclarationDir(module string) string {
	if dir := workspace.moduleDeclarationDirs[module]; dir != "" {
		return dir
	}
	return workspace.Dir()
}

// ComposedWorkspaces returns the resolved children for configuration inheritance.
// The product's environments remain its own; a dependency cannot change the target.
func (workspace *Workspace) ComposedWorkspaces() []*Workspace {
	return append([]*Workspace(nil), workspace.composedWorkspaces...)
}
