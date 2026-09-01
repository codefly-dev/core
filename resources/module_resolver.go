package resources

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
	"gopkg.in/yaml.v3"
)

// LocalOverlayConfigurationName is the gitignored, machine-local file that maps
// a module to where it physically lives on this machine. It is deliberately not
// committed: the workspace's committed config carries module identity (source +
// version), the overlay carries location.
const LocalOverlayConfigurationName = "codefly.local.yaml"

// ModuleResolveDirective is one entry of the overlay's resolve map. Exactly one
// of Path, Worktree, or Pinned selects how the module resolves; they are checked
// in that order.
type ModuleResolveDirective struct {
	// Path is an explicit local directory (absolute, or relative to the overlay
	// file). Highest precedence: you are editing this module in place.
	Path string `yaml:"path,omitempty"`
	// Worktree is "<repo>@<ref>" (e.g. "obin-ai/module-document-store@main"). The
	// resolver scans the local worktree roots for a checkout of <repo> with <ref>
	// checked out, wherever it physically sits.
	Worktree string `yaml:"worktree,omitempty"`
	// Pinned selects the published, base-synced artifact at the committed version.
	Pinned bool `yaml:"pinned,omitempty"`
}

// validate rejects a present overlay entry that does not select exactly one of
// path/worktree/pinned. A present entry means the user intends to override
// resolution, so an empty entry (or one whose only key is a typo yaml silently
// dropped) must be a hard error rather than fall through to committed config and
// silently resolve the wrong way.
func (directive *ModuleResolveDirective) validate(module string) error {
	set := 0
	if directive.Path != "" {
		set++
	}
	if directive.Worktree != "" {
		set++
	}
	if directive.Pinned {
		set++
	}
	if set == 0 {
		return fmt.Errorf("overlay entry for module %q selects none of path/worktree/pinned (check for a typo'd or empty directive)", module)
	}
	if set > 1 {
		return fmt.Errorf("overlay entry for module %q selects more than one of path/worktree/pinned; use exactly one", module)
	}
	return nil
}

// LocalOverlay is the parsed codefly.local.yaml. It maps module name to a
// resolution directive.
type LocalOverlay struct {
	Resolve map[string]*ModuleResolveDirective `yaml:"resolve,omitempty"`

	// dir is the directory the overlay was loaded from; relative Path directives
	// resolve against it.
	dir string
}

// LoadLocalOverlay searches from dir upward to the filesystem root for a
// codefly.local.yaml and loads the first one found. The search order is
// nearest-first: a codefly.local.yaml in the workspace directory wins over one
// in an ancestor. Returns (nil, nil) when no overlay exists anywhere up the
// tree — an absent overlay is the normal case, not an error.
func LoadLocalOverlay(ctx context.Context, dir string) (*LocalOverlay, error) {
	w := wool.Get(ctx).In("resources.LoadLocalOverlay", wool.DirField(dir))
	cur := dir
	for {
		candidate := filepath.Join(cur, LocalOverlayConfigurationName)
		if _, err := os.Stat(candidate); err == nil {
			content, err := os.ReadFile(candidate)
			if err != nil {
				return nil, w.Wrapf(err, "cannot read overlay %s", candidate)
			}
			var overlay LocalOverlay
			if err := yaml.Unmarshal(content, &overlay); err != nil {
				return nil, w.Wrapf(err, "cannot parse overlay %s", candidate)
			}
			overlay.dir = cur
			return &overlay, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return nil, nil
		}
		cur = parent
	}
}

// SaveLocalOverlay writes the overlay as codefly.local.yaml directly in dir
// (it does not search up — a save targets one concrete workspace). Tooling that
// edits the overlay (e.g. `codefly add module --source`) loads, mutates, and
// saves through this pair.
func SaveLocalOverlay(ctx context.Context, dir string, overlay *LocalOverlay) error {
	w := wool.Get(ctx).In("resources.SaveLocalOverlay", wool.DirField(dir))
	content, err := yaml.Marshal(overlay)
	if err != nil {
		return w.Wrapf(err, "cannot marshal overlay")
	}
	file := filepath.Join(dir, LocalOverlayConfigurationName)
	if err := shared.WriteFileAtomic(ctx, file, content, 0o600); err != nil {
		return w.Wrapf(err, "cannot write overlay")
	}
	return nil
}

// ResolutionKind names how a module reference was resolved to a location.
type ResolutionKind string

const (
	// ResolutionLocalPath: the module lives at a concrete local directory (a
	// committed path override, an overlay path, or the in-repo layout default).
	ResolutionLocalPath ResolutionKind = "path"
	// ResolutionWorktree: the module was matched to a local git worktree by
	// repo + ref.
	ResolutionWorktree ResolutionKind = "worktree"
	// ResolutionPinned: the module resolves to a published artifact at Version.
	// Core does not pull artifacts; the CLI resolves a pinned module before it is
	// loaded as a directory.
	ResolutionPinned ResolutionKind = "pinned"
)

// ModuleResolution is the outcome of resolving one ModuleReference against the
// overlay + committed identity. It is what `codefly show dependencies` reports
// per module.
type ModuleResolution struct {
	Module  string
	Kind    ResolutionKind
	Dir     string // resolved directory for path/worktree; empty for pinned
	Source  string // canonical repo identity (worktree/pinned); empty for a pure path
	Version string // committed version constraint (pinned)
	Ref     string // git ref (worktree)
}

// ResolveModule computes where a single module reference resolves, following the
// precedence: overlay path -> overlay worktree -> overlay pinned -> committed
// path override -> committed identity (pinned) -> in-repo layout default. A
// worktree directive that matches no local checkout is an error; a pinned
// outcome is not (it is a valid resolution the CLI acts on).
func (workspace *Workspace) ResolveModule(ctx context.Context, ref *ModuleReference) (*ModuleResolution, error) {
	w := wool.Get(ctx).In("Workspace::ResolveModule", wool.NameField(ref.Name))

	if workspace.overlay != nil {
		if directive := workspace.overlay.Resolve[ref.Name]; directive != nil {
			if err := directive.validate(ref.Name); err != nil {
				return nil, w.Wrap(err)
			}
			switch {
			case directive.Path != "":
				dir := directive.Path
				if !filepath.IsAbs(dir) {
					dir = filepath.Join(workspace.overlay.dir, dir)
				}
				return &ModuleResolution{
					Module: ref.Name,
					Kind:   ResolutionLocalPath,
					Dir:    filepath.Clean(dir),
					Source: ref.Source,
				}, nil
			case directive.Worktree != "":
				repo, gitRef, err := parseWorktreeCoordinate(directive.Worktree)
				if err != nil {
					return nil, w.Wrap(err)
				}
				checkout, err := workspace.findWorktreeCheckout(ctx, repo, gitRef)
				if err != nil {
					return nil, w.Wrapf(err, "cannot resolve worktree for module %q", ref.Name)
				}
				dir := checkout
				if ref.Module != "" {
					dir = filepath.Join(dir, ref.Module)
				}
				return &ModuleResolution{
					Module: ref.Name,
					Kind:   ResolutionWorktree,
					Dir:    dir,
					Source: repo,
					Ref:    gitRef,
				}, nil
			case directive.Pinned:
				return pinnedResolution(ref), nil
			}
		}
	}

	if ref.PathOverride != nil {
		return &ModuleResolution{
			Module: ref.Name,
			Kind:   ResolutionLocalPath,
			Dir:    workspace.ModulePath(ctx, ref),
			Source: ref.Source,
		}, nil
	}
	if ref.Source != "" {
		return pinnedResolution(ref), nil
	}
	return &ModuleResolution{
		Module: ref.Name,
		Kind:   ResolutionLocalPath,
		Dir:    workspace.ModulePath(ctx, ref),
	}, nil
}

// ResolveModules resolves every module reference in the workspace. Unlike the
// load path, a pinned outcome is returned as a normal resolution rather than an
// error, so callers such as `codefly show dependencies` can report the resolved
// source per module.
func (workspace *Workspace) ResolveModules(ctx context.Context) ([]*ModuleResolution, error) {
	w := wool.Get(ctx).In("Workspace::ResolveModules", wool.NameField(workspace.Name))
	resolutions := make([]*ModuleResolution, 0, len(workspace.Modules))
	for _, ref := range workspace.Modules {
		resolution, err := workspace.ResolveModule(ctx, ref)
		if err != nil {
			return nil, w.Wrapf(err, "cannot resolve module %q", ref.Name)
		}
		resolutions = append(resolutions, resolution)
	}
	return resolutions, nil
}

func pinnedResolution(ref *ModuleReference) *ModuleResolution {
	return &ModuleResolution{
		Module:  ref.Name,
		Kind:    ResolutionPinned,
		Source:  ref.Source,
		Version: ref.Version,
	}
}

// parseWorktreeCoordinate splits "<repo>@<ref>" into its repo and ref halves.
func parseWorktreeCoordinate(coordinate string) (string, string, error) {
	repo, ref, found := strings.Cut(coordinate, "@")
	if !found || repo == "" || ref == "" {
		return "", "", fmt.Errorf("worktree coordinate %q must be <repo>@<ref>", coordinate)
	}
	return repo, ref, nil
}

// worktreeContainer walks up from dir to the lazybox worktree container: the
// parent of the nearest ancestor named "github-<org>-<repo>". Worktree roots
// live as "<container>/github-<org>-<repo>/<branch>/…", so the container is
// where sibling checkouts are found. Returns "" when dir is not inside such a
// layout.
func worktreeContainer(dir string) string {
	cur := dir
	for {
		if strings.HasPrefix(filepath.Base(cur), "github-") {
			return filepath.Dir(cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// worktreeCheckout is one local git checkout discovered under the worktree
// container: its checkout-root directory, its normalized origin repo, and the
// branch currently checked out ("HEAD" when detached).
type worktreeCheckout struct {
	root   string
	repo   string
	branch string
}

// findWorktreeCheckout resolves repo@ref to a local checkout-root directory.
// Identity is confirmed by the checkout's own origin remote (authoritative — the
// directory-name convention only says where to look). An exact branch-name match
// is preferred; otherwise any checkout whose HEAD resolves to ref is used.
// Ambiguity (more than one match at the same precedence) is a hard error rather
// than a silent pick.
func (workspace *Workspace) findWorktreeCheckout(ctx context.Context, repo, ref string) (string, error) {
	checkouts, err := workspace.worktreeCheckouts(ctx)
	if err != nil {
		return "", err
	}
	wantRepo := normalizeRepo(repo)

	var exact, fallback []string
	for _, checkout := range checkouts {
		if checkout.repo != wantRepo {
			continue
		}
		if checkout.branch == ref {
			exact = append(exact, checkout.root)
		} else if gitRefAtHead(ctx, checkout.root, ref) {
			fallback = append(fallback, checkout.root)
		}
	}

	for _, matches := range [][]string{exact, fallback} {
		switch len(matches) {
		case 0:
			continue
		case 1:
			return matches[0], nil
		default:
			return "", fmt.Errorf("worktree %s@%s is ambiguous: matches %s", repo, ref, strings.Join(matches, ", "))
		}
	}
	return "", fmt.Errorf("no local worktree of %s has %s checked out under the worktree container", repo, ref)
}

// worktreeCheckouts enumerates every local git checkout-root under the worktree
// container, once per workspace. A candidate counts only when git's own toplevel
// for it is the candidate itself: a directory that is not its own checkout root
// (an empty dir, or one nested inside some ancestor repo) resolves upward to a
// different toplevel and must be rejected, or it would bind a module to the
// wrong — possibly ancestor — repository.
func (workspace *Workspace) worktreeCheckouts(ctx context.Context) ([]worktreeCheckout, error) {
	if workspace.worktreeScanned {
		return workspace.worktreeScan, nil
	}
	container := worktreeContainer(workspace.Dir())
	if container == "" {
		return nil, fmt.Errorf("cannot locate worktree container from %q (expected a github-<org>-<repo>/<branch> layout)", workspace.Dir())
	}
	orgDirs, err := os.ReadDir(container)
	if err != nil {
		return nil, fmt.Errorf("cannot read worktree container %q: %w", container, err)
	}
	var checkouts []worktreeCheckout
	for _, orgDir := range orgDirs {
		if !orgDir.IsDir() || !strings.HasPrefix(orgDir.Name(), "github-") {
			continue
		}
		branchDirs, err := os.ReadDir(filepath.Join(container, orgDir.Name()))
		if err != nil {
			continue
		}
		for _, branchDir := range branchDirs {
			if !branchDir.IsDir() {
				continue
			}
			candidate := filepath.Join(container, orgDir.Name(), branchDir.Name())
			root, ok := gitToplevel(ctx, candidate)
			if !ok || !sameDir(root, candidate) {
				continue
			}
			repo, ok := gitOriginRepo(ctx, root)
			if !ok {
				continue
			}
			checkouts = append(checkouts, worktreeCheckout{
				root:   root,
				repo:   repo,
				branch: gitBranch(ctx, root),
			})
		}
	}
	workspace.worktreeScan = checkouts
	workspace.worktreeScanned = true
	return checkouts, nil
}

// sameDir reports whether two paths denote the same directory, resolving
// symlinks first so that e.g. a /var vs /private/var difference (macOS temp) or
// a symlinked checkout does not read as distinct. git's toplevel is already
// symlink-resolved; the scanned candidate may not be.
func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return ra == rb
}

// normalizeRepo reduces a git remote URL (or a bare "org/repo") to a lowercase
// "org/repo" so SSH and HTTPS remotes for the same repository compare equal.
func normalizeRepo(remote string) string {
	s := strings.TrimSpace(remote)
	s = strings.TrimSuffix(s, ".git")
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if at := strings.Index(s, "@"); at >= 0 {
			s = s[at+1:]
		}
		if slash := strings.Index(s, "/"); slash >= 0 {
			s = s[slash+1:]
		}
	} else if at := strings.Index(s, "@"); at >= 0 {
		// scp-like syntax: git@github.com:org/repo
		s = s[at+1:]
		if colon := strings.Index(s, ":"); colon >= 0 {
			s = s[colon+1:]
		}
	}
	return strings.ToLower(strings.Trim(s, "/"))
}

func gitToplevel(ctx context.Context, dir string) (string, bool) {
	out, ok := gitOutput(ctx, dir, "rev-parse", "--show-toplevel")
	if !ok {
		return "", false
	}
	return out, true
}

func gitOriginRepo(ctx context.Context, dir string) (string, bool) {
	out, ok := gitOutput(ctx, dir, "remote", "get-url", "origin")
	if !ok {
		return "", false
	}
	return normalizeRepo(out), true
}

func gitBranch(ctx context.Context, dir string) string {
	out, ok := gitOutput(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		return ""
	}
	return out
}

// gitRefAtHead reports whether ref resolves to the same commit as HEAD in dir,
// covering the case where the wanted ref is a tag or sha rather than the branch
// name.
func gitRefAtHead(ctx context.Context, dir, ref string) bool {
	head, ok := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if !ok {
		return false
	}
	target, ok := gitOutput(ctx, dir, "rev-parse", "--verify", ref+"^{commit}")
	if !ok {
		return false
	}
	return head == target
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, bool) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}
