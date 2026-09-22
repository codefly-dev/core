package resources

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
// of Path, Worktree, Pinned, or Git selects how the module resolves; they are
// checked in that order.
type ModuleResolveDirective struct {
	// Path is an explicit local directory (absolute, or relative to the overlay
	// file). Highest precedence: you are editing this module in place.
	Path string `yaml:"path,omitempty"`
	// Worktree is "<repo>@<ref>" (e.g. "obin-ai/module-document-store@main"). The
	// resolver scans the local worktree roots for a checkout of <repo> that is at
	// <ref>, wherever it physically sits. A checkout matches when <ref> is the
	// checked-out branch name, or — for a tag, sha, or a detached HEAD created by
	// `git worktree add <dir> origin/<ref>` — when its HEAD commit is <ref>. The
	// match is thus by resolved commit, not by which branch happens to be checked
	// out: a differently-named branch sitting at <ref>'s commit also matches, and
	// stops matching once it commits away from that point.
	Worktree string `yaml:"worktree,omitempty"`
	// Pinned selects the published, base-synced artifact at the committed version.
	Pinned bool `yaml:"pinned,omitempty"`
	// Git selects a clone of the module's source repository at the committed
	// version, bypassing artifact verification. It is the escape hatch for a
	// source-referenced module with no pullable signed artifact.
	Git bool `yaml:"git,omitempty"`
	// Services overrides where individual services of the module come from,
	// leaving the rest of it wherever the module directive puts it. It is
	// orthogonal to the selectors above: an entry carrying only Services keeps
	// committed module resolution untouched.
	Services map[string]*ServiceResolveDirective `yaml:"services,omitempty"`
}

// ServiceResolveDirective is one entry of a module directive's services map: it
// says where a single service of a composed module lives on this machine, while
// the rest of the module resolves as it otherwise would. Exactly one of Path,
// Worktree, or Version selects how the service resolves; they are checked in
// that order.
type ServiceResolveDirective struct {
	// Path is an explicit local directory (absolute, or relative to the overlay
	// file) holding the service.
	Path string `yaml:"path,omitempty"`
	// Worktree is "<repo>@<ref>", matched to a local checkout exactly as the
	// module-level directive is. The service is taken from that checkout's copy
	// of the module, at services/<name>.
	Worktree string `yaml:"worktree,omitempty"`
	// Version selects the module package at that version and takes the service
	// from it. Core does not pull packages: the resolution is reported as pinned
	// and the CLI materializes it.
	Version string `yaml:"version,omitempty"`
}

// validate rejects a service entry that does not select exactly one of
// path/worktree/version, for the same reason the module directive does: a
// present entry means the user intends an override, so a typo'd or empty one
// must fail loudly rather than silently leave the service where it was.
func (directive *ServiceResolveDirective) validate(module, service string) error {
	set := 0
	for _, selected := range []bool{directive.Path != "", directive.Worktree != "", directive.Version != ""} {
		if selected {
			set++
		}
	}
	if set == 0 {
		return fmt.Errorf("overlay entry for service %q of module %q selects none of path/worktree/version (check for a typo'd or empty directive)", service, module)
	}
	if set > 1 {
		return fmt.Errorf("overlay entry for service %q of module %q selects more than one of path/worktree/version; use exactly one", service, module)
	}
	return nil
}

// validate rejects a present overlay entry that does not select exactly one of
// path/worktree/pinned/git. A present entry means the user intends to override
// resolution, so an empty entry (or one whose only key is a typo yaml silently
// dropped) must be a hard error rather than fall through to committed config and
// silently resolve the wrong way. An entry carrying only service overrides is
// the exception: it overrides services, not the module, so module resolution
// follows committed config exactly as if the entry were absent.
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
	if directive.Git {
		set++
	}
	if set == 0 && len(directive.Services) == 0 {
		return fmt.Errorf("overlay entry for module %q selects none of path/worktree/pinned/git/services (check for a typo'd or empty directive)", module)
	}
	if set > 1 {
		return fmt.Errorf("overlay entry for module %q selects more than one of path/worktree/pinned/git; use exactly one", module)
	}
	for _, service := range sortedKeys(directive.Services) {
		if err := directive.Services[service].validate(module, service); err != nil {
			return err
		}
	}
	return nil
}

// sortedKeys orders a map's keys so that iteration — and the errors it
// produces — is stable across runs.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	// Unverified marks a pinned resolution the overlay opted out of artifact
	// verification: the CLI materializes it by cloning Source rather than by
	// pulling the signed artifact. It refines a pinned resolution rather than
	// being a kind of its own, so a consumer that only asks "is this pinned"
	// still routes it through materialization instead of reading an empty Dir.
	Unverified bool
	// Services holds the per-service overrides the overlay asked for, keyed by
	// service name. They refine where individual services of this module come
	// from; the module resolution above still decides where the rest of it does.
	Services map[string]*ServiceResolution
}

// ServiceResolution is the outcome of resolving one service override against the
// overlay. A path or worktree directive yields a concrete Dir; a version
// directive yields a pinned resolution the CLI materializes, exactly as it does
// for a pinned module.
type ServiceResolution struct {
	Service string
	Kind    ResolutionKind
	Dir     string // resolved directory for path/worktree; empty for pinned
	Source  string // canonical repo identity (worktree/pinned); empty for a pure path
	Version string // requested package version (pinned)
	Ref     string // git ref (worktree)
}

// ResolveModule computes where a single module reference resolves, following the
// precedence: overlay path -> overlay worktree -> overlay pinned -> overlay git
// -> committed path override -> committed identity (pinned) -> in-repo layout
// default. A worktree directive that matches no local checkout is an error; a
// pinned outcome is not (it is a valid resolution the CLI acts on). An overlay
// git directive resolves pinned too, marked Unverified: it selects how the
// module is materialized, not a different place for it to live.
//
// Per-service overrides ride alongside that outcome in Services: they say where
// individual services come from without moving the module, so they apply to
// every module resolution, including the committed fall-through.
func (workspace *Workspace) ResolveModule(ctx context.Context, ref *ModuleReference) (*ModuleResolution, error) {
	w := wool.Get(ctx).In("Workspace::ResolveModule", wool.NameField(ref.Name))

	var directive *ModuleResolveDirective
	if workspace.overlay != nil {
		directive = workspace.overlay.Resolve[ref.Name]
	}
	if directive != nil {
		if err := directive.validate(ref.Name); err != nil {
			return nil, w.Wrap(err)
		}
	}
	resolution, err := workspace.resolveModuleLocation(ctx, ref, directive)
	if err != nil {
		return nil, w.Wrap(err)
	}
	if directive != nil {
		resolution.Services, err = workspace.resolveServices(ctx, ref, directive.Services)
		if err != nil {
			return nil, w.Wrapf(err, "cannot resolve service overrides for module %q", ref.Name)
		}
	}
	return resolution, nil
}

// resolveModuleLocation decides where the module itself comes from, following
// the precedence documented on ResolveModule. Service overrides never enter
// here: they refine services, not the module, so an overlay entry carrying only
// services falls through to committed config.
func (workspace *Workspace) resolveModuleLocation(ctx context.Context, ref *ModuleReference, directive *ModuleResolveDirective) (*ModuleResolution, error) {
	w := wool.Get(ctx).In("Workspace::resolveModuleLocation", wool.NameField(ref.Name))
	if directive != nil {
		switch {
		case directive.Path != "":
			return &ModuleResolution{
				Module: ref.Name,
				Kind:   ResolutionLocalPath,
				Dir:    workspace.overlayDir(directive.Path),
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
		case directive.Git:
			if ref.Source == "" {
				return nil, w.NewError("overlay entry for module <%s> selects git, but the module is composed without a source to clone; give it a source, or point the overlay at a local path", ref.Name)
			}
			resolution := pinnedResolution(ref)
			resolution.Unverified = true
			return resolution, nil
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

// resolveServices resolves every per-service override declared for one module.
func (workspace *Workspace) resolveServices(ctx context.Context, ref *ModuleReference, directives map[string]*ServiceResolveDirective) (map[string]*ServiceResolution, error) {
	if len(directives) == 0 {
		return nil, nil
	}
	resolutions := make(map[string]*ServiceResolution, len(directives))
	for _, service := range sortedKeys(directives) {
		resolution, err := workspace.resolveService(ctx, ref, service, directives[service])
		if err != nil {
			return nil, err
		}
		resolutions[service] = resolution
	}
	return resolutions, nil
}

// resolveService resolves one service override. The worktree and version forms
// address the service through the module that owns it — a checkout, or a package
// of the module reference's own coordinate — so the service keeps arriving with
// the module layout its siblings expect.
func (workspace *Workspace) resolveService(ctx context.Context, ref *ModuleReference, service string, directive *ServiceResolveDirective) (*ServiceResolution, error) {
	w := wool.Get(ctx).In("Workspace::resolveService", wool.NameField(service))
	switch {
	case directive.Path != "":
		return &ServiceResolution{
			Service: service,
			Kind:    ResolutionLocalPath,
			Dir:     workspace.overlayDir(directive.Path),
		}, nil
	case directive.Worktree != "":
		repo, gitRef, err := parseWorktreeCoordinate(directive.Worktree)
		if err != nil {
			return nil, w.Wrap(err)
		}
		checkout, err := workspace.findWorktreeCheckout(ctx, repo, gitRef)
		if err != nil {
			return nil, w.Wrapf(err, "cannot resolve worktree for service %q", service)
		}
		dir := checkout
		if ref.Module != "" {
			dir = filepath.Join(dir, ref.Module)
		}
		return &ServiceResolution{
			Service: service,
			Kind:    ResolutionWorktree,
			Dir:     filepath.Join(dir, "services", service),
			Source:  repo,
			Ref:     gitRef,
		}, nil
	default:
		if ref.Source == "" {
			return nil, w.NewError("overlay entry for service %q of module <%s> selects a version, but the module is composed without a source to pull it from; point the override at a local path instead", service, ref.Name)
		}
		return &ServiceResolution{
			Service: service,
			Kind:    ResolutionPinned,
			Source:  ref.Source,
			Version: directive.Version,
		}, nil
	}
}

// overlayDir makes a directive's path absolute: an absolute path stands, a
// relative one resolves against the overlay file's own directory.
func (workspace *Workspace) overlayDir(dir string) string {
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(workspace.overlay.dir, dir)
	}
	return filepath.Clean(dir)
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

	var exact, fallback, sameRepo []worktreeCheckout
	for _, checkout := range checkouts {
		if checkout.repo != wantRepo {
			continue
		}
		sameRepo = append(sameRepo, checkout)
		if checkout.branch == ref {
			exact = append(exact, checkout)
		} else if gitRefAtHead(ctx, checkout.root, ref, detachedHead(checkout.branch)) {
			fallback = append(fallback, checkout)
		}
	}

	for _, matches := range [][]worktreeCheckout{exact, fallback} {
		switch len(matches) {
		case 0:
			continue
		case 1:
			return matches[0].root, nil
		default:
			return "", fmt.Errorf("worktree %s@%s is ambiguous: matches %s", repo, ref, strings.Join(checkoutRoots(matches), ", "))
		}
	}
	if len(sameRepo) > 0 {
		var states []string
		for _, checkout := range sameRepo {
			states = append(states, fmt.Sprintf("%s (%s)", checkout.root, describeCheckoutState(ctx, checkout)))
		}
		return "", fmt.Errorf("found a checkout of %s but none has %s checked out: %s; check out a branch named %s, or pin the ref to the matching @<sha|tag>",
			repo, ref, strings.Join(states, ", "), ref)
	}
	return "", fmt.Errorf("no local worktree of %s has %s checked out under the worktree container", repo, ref)
}

func checkoutRoots(checkouts []worktreeCheckout) []string {
	roots := make([]string, len(checkouts))
	for i, checkout := range checkouts {
		roots[i] = checkout.root
	}
	return roots
}

// describeCheckoutState renders why a checkout of the right repo didn't match the
// wanted ref: "on branch <x>" for a named branch, or "detached at <short-sha>" for
// a detached HEAD.
func describeCheckoutState(ctx context.Context, checkout worktreeCheckout) string {
	if !detachedHead(checkout.branch) {
		return "on branch " + checkout.branch
	}
	if head, ok := gitOutput(ctx, checkout.root, "rev-parse", "--short", "HEAD"); ok {
		return "detached at " + head
	}
	return "detached"
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
			if !ok || !SameDir(root, candidate) {
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

// SameDir reports whether two paths denote the same directory. Lexically equal
// paths are the same directory (this also covers paths that do not exist yet,
// which EvalSymlinks cannot resolve); otherwise it resolves symlinks so that a
// /var vs /private/var difference (macOS temp) or a symlinked checkout does not
// read as distinct. git's toplevel is already symlink-resolved; a scanned
// candidate may not be.
func SameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
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

// gitRefAtHead reports whether ref resolves to the same commit as HEAD in dir.
// It covers a wanted ref that is a tag or sha. When the checkout is detached (it
// has no branch identity of its own), it also matches a branch that exists only
// as a remote-tracking ref — the case for a worktree created with
// `git worktree add <dir> origin/<ref>`, where no local branch named <ref> exists
// but origin/<ref> points at the checked-out commit.
//
// For an on-branch checkout the remote-tracking ref is deliberately NOT
// consulted: the branch name is the checkout's identity, and matching
// origin/<ref> there would bind @<ref> to a worktree sitting on a different
// branch that merely shares a commit with origin/<ref> (e.g. right after
// branching, before either side advances).
func gitRefAtHead(ctx context.Context, dir, ref string, detached bool) bool {
	head, ok := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if !ok {
		return false
	}
	candidates := []string{ref}
	if detached {
		candidates = append(candidates, "origin/"+ref)
	}
	for _, candidate := range candidates {
		if target, ok := gitOutput(ctx, dir, "rev-parse", "--verify", candidate+"^{commit}"); ok && target == head {
			return true
		}
	}
	return false
}

// detachedHead reports whether a checkout's HEAD is detached, given the branch
// name reported by gitBranch ("HEAD" for a detached HEAD, "" when git could not
// report it).
func detachedHead(branch string) bool {
	return branch == "" || branch == "HEAD"
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, bool) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}
