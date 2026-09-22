package resources_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/internal/testgit"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestLoadLocalOverlaySearchesUp(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  documents:\n    worktree: acme/docs@main\n"), 0o600))

	overlay, err := resources.LoadLocalOverlay(ctx, nested)
	require.NoError(t, err)
	require.NotNil(t, overlay)
	require.Contains(t, overlay.Resolve, "documents")
	require.Equal(t, "acme/docs@main", overlay.Resolve["documents"].Worktree)
}

func TestLoadLocalOverlayNearestWins(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	nested := filepath.Join(root, "workspace")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  m:\n    pinned: true\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(nested, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  m:\n    path: .\n"), 0o600))

	overlay, err := resources.LoadLocalOverlay(ctx, nested)
	require.NoError(t, err)
	require.NotNil(t, overlay)
	require.Equal(t, ".", overlay.Resolve["m"].Path)
	require.False(t, overlay.Resolve["m"].Pinned)
}

func TestLoadLocalOverlayAbsentIsNotError(t *testing.T) {
	ctx := context.Background()
	overlay, err := resources.LoadLocalOverlay(ctx, t.TempDir())
	require.NoError(t, err)
	require.Nil(t, overlay)
}

func TestSaveLocalOverlayRoundTrips(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	overlay := &resources.LocalOverlay{
		Resolve: map[string]*resources.ModuleResolveDirective{
			"documents": {Worktree: "acme/docs@main"},
			"saas":      {Pinned: true},
			"billing":   {Git: true},
		},
	}
	require.NoError(t, resources.SaveLocalOverlay(ctx, dir, overlay))

	reloaded, err := resources.LoadLocalOverlay(ctx, dir)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	require.Equal(t, "acme/docs@main", reloaded.Resolve["documents"].Worktree)
	require.True(t, reloaded.Resolve["saas"].Pinned)
	require.True(t, reloaded.Resolve["billing"].Git)
}

// The documented escape hatch: a git-only overlay entry is a valid selection and
// resolves pinned, marked Unverified. It stays a pinned resolution on purpose —
// a consumer that asks only "is this pinned" must still route it through
// materialization rather than read its empty Dir as a real directory.
func TestOverlayGitDirectiveResolvesPinnedUnverified(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host\n    version: \">=0.0.44\"\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    git: true\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	resolution, err := workspace.ResolveModule(ctx, workspace.Modules[0])
	require.NoError(t, err)
	require.Equal(t, resources.ResolutionPinned, resolution.Kind)
	require.True(t, resolution.Unverified)
	require.Equal(t, "acme/host", resolution.Source)
	require.Equal(t, ">=0.0.44", resolution.Version)
	require.Empty(t, resolution.Dir)

	_, err = workspace.LoadModuleFromName(ctx, "saas")
	require.ErrorContains(t, err, "not loadable as a local checkout")
}

// A pinned resolution that the overlay did not opt out of verification must not
// carry Unverified: the flag is what tells the CLI to clone instead of pulling
// the signed artifact, so a stray true would silently downgrade trust.
func TestPinnedResolutionIsVerifiedUnlessOverlaySaysGit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host\n    version: \"1.0\"\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    pinned: true\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	resolution, err := workspace.ResolveModule(ctx, workspace.Modules[0])
	require.NoError(t, err)
	require.Equal(t, resources.ResolutionPinned, resolution.Kind)
	require.False(t, resolution.Unverified)
}

// git: true says "clone this module's source instead of pulling its artifact".
// A module composed without a source has nothing to clone, so the directive is
// unsatisfiable and must fail loudly at resolution rather than resolve to a
// sourceless pinned outcome the CLI would quietly skip.
func TestOverlayGitDirectiveRequiresSource(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: platform\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  platform:\n    git: true\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	_, err = workspace.ResolveModule(ctx, workspace.Modules[0])
	require.ErrorContains(t, err, "platform")
	require.ErrorContains(t, err, "without a source to clone")
}

// Every directive kind reported side by side, which is what `show dependencies`
// renders for a mixed overlay. The git entry must not report as a resolution
// carrying a directory.
func TestResolveModulesReportsMixedOverlay(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: local\n    source: acme/local\n  - name: saas\n    source: acme/host\n    version: \"1.0\"\n  - name: docs\n    source: acme/docs\n    version: \"2.0\"\n")
	writeModule(t, filepath.Join(dir, "here"), "kind: module\nname: local\nservices: []\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  local:\n    path: here\n  saas:\n    pinned: true\n  docs:\n    git: true\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	resolutions, err := workspace.ResolveModules(ctx)
	require.NoError(t, err)
	byName := map[string]*resources.ModuleResolution{}
	for _, r := range resolutions {
		byName[r.Module] = r
	}
	require.Equal(t, resources.ResolutionLocalPath, byName["local"].Kind)
	require.Equal(t, filepath.Join(dir, "here"), byName["local"].Dir)
	require.Equal(t, resources.ResolutionPinned, byName["saas"].Kind)
	require.False(t, byName["saas"].Unverified)
	require.Equal(t, resources.ResolutionPinned, byName["docs"].Kind)
	require.True(t, byName["docs"].Unverified)
	require.Empty(t, byName["docs"].Dir)
	require.Equal(t, "acme/docs", byName["docs"].Source)
}

// A git directive survives the load -> save round trip the CLI performs when it
// materializes other modules. Before core knew the key, marshalling the typed
// overlay back dropped it and rewrote the entry as an empty map — which the
// validator then rejects, destroying the user's escape hatch on an unrelated
// write.
func TestSaveLocalOverlayPreservesGitDirectiveFromFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    git: true\n"), 0o600))

	overlay, err := resources.LoadLocalOverlay(ctx, dir)
	require.NoError(t, err)
	overlay.Resolve["other"] = &resources.ModuleResolveDirective{Path: "somewhere"}
	require.NoError(t, resources.SaveLocalOverlay(ctx, dir, overlay))

	reloaded, err := resources.LoadLocalOverlay(ctx, dir)
	require.NoError(t, err)
	require.True(t, reloaded.Resolve["saas"].Git)
}

// An identity-only reference with no overlay and no local checkout resolves to a
// pinned artifact. Core does not pull artifacts, so loading it as a directory is
// a clear error rather than a wrong path.
func TestIdentityOnlyModuleResolvesPinned(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: platform\n  - name: saas\n    source: acme/host\n    version: \">=0.0.44\"\n")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	resolutions, err := workspace.ResolveModules(ctx)
	require.NoError(t, err)
	byName := map[string]*resources.ModuleResolution{}
	for _, r := range resolutions {
		byName[r.Module] = r
	}
	require.Equal(t, resources.ResolutionPinned, byName["saas"].Kind)
	require.Equal(t, "acme/host", byName["saas"].Source)
	require.Equal(t, ">=0.0.44", byName["saas"].Version)

	_, err = workspace.LoadModuleFromName(ctx, "saas")
	require.ErrorContains(t, err, "pinned")
}

// The overlay's explicit path directive wins: a module composed by identity is
// resolved to an editable local checkout, wherever the overlay points it.
func TestOverlayPathDirectiveResolvesModule(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host\n")

	host := filepath.Join(dir, "elsewhere", "host")
	writeModule(t, host, "kind: module\nname: saas\nservices:\n  - name: gateway\n")
	writeGatewayService(t, host)

	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    path: elsewhere/host\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	resolution, err := workspace.ResolveModule(ctx, workspace.Modules[0])
	require.NoError(t, err)
	require.Equal(t, resources.ResolutionLocalPath, resolution.Kind)
	require.Equal(t, filepath.Join(dir, "elsewhere", "host"), resolution.Dir)

	saas, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)
	gateway, err := saas.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	require.Equal(t, "gateway", gateway.Name)
}

// End to end: an identity-only committed reference plus a worktree overlay boots
// against a real sibling git checkout matched by remote + ref — no committed
// path, no symlink. This is the core of the worktree-aware resolver.
func TestOverlayWorktreeResolvesAgainstRealCheckout(t *testing.T) {
	ctx := context.Background()
	container := t.TempDir()

	// Consumer workspace lives at container/github-acme-solution/main.
	solution := filepath.Join(container, "github-acme-solution", "main")
	writeWorkspace(t, solution, "name: solution\nlayout: modules\nmodules:\n  - name: platform\n  - name: saas\n    source: acme/host\n")
	platform := filepath.Join(solution, "modules", "platform")
	writeModule(t, platform, "kind: module\nname: platform\nservices:\n  - name: api\n")
	writeAPIService(t, platform)
	require.NoError(t, os.WriteFile(filepath.Join(solution, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    worktree: acme/host@main\n"), 0o600))

	// Host checkout lives at container/github-acme-host/main as a real git repo
	// whose origin is acme/host on branch main.
	host := filepath.Join(container, "github-acme-host", "main")
	writeModule(t, host, "kind: module\nname: saas\nservices:\n  - name: gateway\n")
	writeGatewayService(t, host)
	initGitRepo(t, host, "git@github.com:acme/host.git", "main")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, solution)
	require.NoError(t, err)

	resolution, err := workspace.ResolveModule(ctx, mustModuleRef(t, workspace, "saas"))
	require.NoError(t, err)
	require.Equal(t, resources.ResolutionWorktree, resolution.Kind)
	require.Equal(t, "acme/host", resolution.Source)
	require.Equal(t, "main", resolution.Ref)
	realHost, err := filepath.EvalSymlinks(host)
	require.NoError(t, err)
	resolvedDir, err := filepath.EvalSymlinks(resolution.Dir)
	require.NoError(t, err)
	require.Equal(t, realHost, resolvedDir)

	// The composed module and its service load across the worktree boundary.
	saas, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)
	gateway, err := saas.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	require.Len(t, gateway.Endpoints, 1)
	require.Equal(t, "public-api", gateway.Endpoints[0].Name)

	// The in-repo consumer depends on the worktree producer's public endpoint;
	// visibility wiring resolves across the boundary.
	require.NoError(t, workspace.ValidateServiceDependencies(ctx))
}

// Two worktree-sourced modules resolve against two different sibling checkouts
// through the single cached container scan — the cache must serve each distinct
// repo correctly, not collapse to the first.
func TestMultipleWorktreeModulesResolveThroughCachedScan(t *testing.T) {
	ctx := context.Background()
	container := t.TempDir()

	solution := filepath.Join(container, "github-acme-solution", "main")
	writeWorkspace(t, solution, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host-a\n  - name: docs\n    source: acme/host-b\n")
	require.NoError(t, os.WriteFile(filepath.Join(solution, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    worktree: acme/host-a@main\n  docs:\n    worktree: acme/host-b@main\n"), 0o600))

	hostA := filepath.Join(container, "github-acme-host-a", "main")
	writeModule(t, hostA, "kind: module\nname: saas\nservices:\n  - name: gateway\n")
	writeGatewayService(t, hostA)
	initGitRepo(t, hostA, "git@github.com:acme/host-a.git", "main")

	hostB := filepath.Join(container, "github-acme-host-b", "main")
	writeModule(t, hostB, "kind: module\nname: docs\nservices: []\n")
	initGitRepo(t, hostB, "git@github.com:acme/host-b.git", "main")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, solution)
	require.NoError(t, err)

	saas, err := workspace.ResolveModule(ctx, mustModuleRef(t, workspace, "saas"))
	require.NoError(t, err)
	docs, err := workspace.ResolveModule(ctx, mustModuleRef(t, workspace, "docs"))
	require.NoError(t, err)

	realA, _ := filepath.EvalSymlinks(hostA)
	realB, _ := filepath.EvalSymlinks(hostB)
	gotA, _ := filepath.EvalSymlinks(saas.Dir)
	gotB, _ := filepath.EvalSymlinks(docs.Dir)
	require.Equal(t, realA, gotA)
	require.Equal(t, realB, gotB)
}

// A worktree directive that matches no local checkout is a hard, descriptive
// error, not a silent fallback.
func TestOverlayWorktreeNoMatchErrors(t *testing.T) {
	ctx := context.Background()
	container := t.TempDir()
	solution := filepath.Join(container, "github-acme-solution", "main")
	writeWorkspace(t, solution, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host\n")
	require.NoError(t, os.WriteFile(filepath.Join(solution, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    worktree: acme/host@main\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, solution)
	require.NoError(t, err)

	_, err = workspace.LoadModuleFromName(ctx, "saas")
	require.ErrorContains(t, err, "no local worktree")
}

// A present overlay entry that selects no directive — an empty entry, or one
// whose only key is a typo yaml silently dropped — must be a hard error, not a
// silent fall-through to committed config that resolves the module the wrong
// way. Without this the user's override becomes a no-op with no diagnostic.
func TestOverlayDirectiveMustSelectExactlyOne(t *testing.T) {
	ctx := context.Background()

	cases := map[string]string{
		"typo'd key":   "resolve:\n  saas:\n    worktee: acme/host@main\n",
		"empty entry":  "resolve:\n  saas: {}\n",
		"two selected": "resolve:\n  saas:\n    pinned: true\n    path: .\n",
		"git and path": "resolve:\n  saas:\n    git: true\n    path: .\n",
	}
	for name, overlay := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host\n    version: \"1.0\"\n")
			require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName), []byte(overlay), 0o600))

			workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
			require.NoError(t, err)

			_, err = workspace.ResolveModule(ctx, workspace.Modules[0])
			require.Error(t, err)
			require.ErrorContains(t, err, "saas")
		})
	}
}

// A candidate dir that is not its own checkout root — here a non-git placeholder
// nested inside an ancestor git repo whose origin coincidentally matches the
// wanted repo — must NOT be treated as a match. git's toplevel for it resolves
// up to the ancestor, and binding the module there would load the wrong
// directory. The scan requires toplevel == candidate, so this resolves to a
// clean "no local worktree" error instead.
func TestWorktreeIgnoresNonCheckoutNestedInAncestorRepo(t *testing.T) {
	ctx := context.Background()
	container := t.TempDir()

	solution := filepath.Join(container, "github-acme-solution", "main")
	writeWorkspace(t, solution, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host\n")
	require.NoError(t, os.WriteFile(filepath.Join(solution, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    worktree: acme/host@main\n"), 0o600))

	// A placeholder branch dir with no .git of its own.
	require.NoError(t, os.MkdirAll(filepath.Join(container, "github-acme-host", "main"), 0o755))

	// The whole container is a git repo whose origin is exactly the wanted repo.
	// Without the toplevel==candidate guard, the non-git placeholder would resolve
	// up to this repo and be picked — a wrong-directory bind.
	initGitRepo(t, container, "git@github.com:acme/host.git", "main")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, solution)
	require.NoError(t, err)

	_, err = workspace.LoadModuleFromName(ctx, "saas")
	require.ErrorContains(t, err, "no local worktree")
}

// A detached worktree (`git worktree add <dir> origin/main`) has HEAD at the
// commit of origin/main but no local branch named main. The directive
// worktree:<repo>@main must resolve it by matching HEAD against origin/main, not
// only against a branch literally named main.
func TestOverlayWorktreeResolvesDetachedHeadAtRemoteRef(t *testing.T) {
	ctx := context.Background()
	container := t.TempDir()

	solution := filepath.Join(container, "github-acme-solution", "main")
	writeWorkspace(t, solution, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host\n")
	require.NoError(t, os.WriteFile(filepath.Join(solution, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    worktree: acme/host@main\n"), 0o600))

	host := filepath.Join(container, "github-acme-host", "main")
	writeModule(t, host, "kind: module\nname: saas\nservices:\n  - name: gateway\n")
	writeGatewayService(t, host)
	initDetachedRepoAtRemoteRef(t, host, "git@github.com:acme/host.git", "main")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, solution)
	require.NoError(t, err)

	resolution, err := workspace.ResolveModule(ctx, mustModuleRef(t, workspace, "saas"))
	require.NoError(t, err)
	require.Equal(t, resources.ResolutionWorktree, resolution.Kind)
	realHost, _ := filepath.EvalSymlinks(host)
	gotDir, _ := filepath.EvalSymlinks(resolution.Dir)
	require.Equal(t, realHost, gotDir)
}

// A worktree checked out at a tag resolves through @<tag>: the tag is not a
// branch name, so the match is by HEAD commit.
func TestOverlayWorktreeResolvesTag(t *testing.T) {
	ctx := context.Background()
	container := t.TempDir()

	solution := filepath.Join(container, "github-acme-solution", "main")
	writeWorkspace(t, solution, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host\n")
	require.NoError(t, os.WriteFile(filepath.Join(solution, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    worktree: acme/host@v0.0.1\n"), 0o600))

	host := filepath.Join(container, "github-acme-host", "release")
	writeModule(t, host, "kind: module\nname: saas\nservices:\n  - name: gateway\n")
	writeGatewayService(t, host)
	initGitRepo(t, host, "git@github.com:acme/host.git", "work")
	gitRun(t, host, "tag", "v0.0.1")
	gitRun(t, host, "checkout", "--detach", "v0.0.1")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, solution)
	require.NoError(t, err)

	resolution, err := workspace.ResolveModule(ctx, mustModuleRef(t, workspace, "saas"))
	require.NoError(t, err)
	require.Equal(t, resources.ResolutionWorktree, resolution.Kind)
	realHost, _ := filepath.EvalSymlinks(host)
	gotDir, _ := filepath.EvalSymlinks(resolution.Dir)
	require.Equal(t, realHost, gotDir)
}

// When a checkout of the wanted repo exists but sits on a different branch (or is
// detached at a different commit), the error names that checkout and its state so
// the cause is obvious, instead of the bare "no local worktree" message.
func TestOverlayWorktreeMismatchErrorNamesCheckout(t *testing.T) {
	ctx := context.Background()
	container := t.TempDir()

	solution := filepath.Join(container, "github-acme-solution", "main")
	writeWorkspace(t, solution, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host\n")
	require.NoError(t, os.WriteFile(filepath.Join(solution, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    worktree: acme/host@main\n"), 0o600))

	host := filepath.Join(container, "github-acme-host", "dev")
	writeModule(t, host, "kind: module\nname: saas\nservices:\n  - name: gateway\n")
	writeGatewayService(t, host)
	initGitRepo(t, host, "git@github.com:acme/host.git", "dev")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, solution)
	require.NoError(t, err)

	_, err = workspace.LoadModuleFromName(ctx, "saas")
	require.ErrorContains(t, err, "found a checkout of acme/host")
	require.ErrorContains(t, err, "on branch dev")
	require.ErrorContains(t, err, "main")
}

// An on-branch worktree whose HEAD coincidentally sits at origin/<other-ref>'s
// commit must NOT resolve for worktree:<repo>@<other-ref>. The branch name is the
// checkout's identity; consulting the remote-tracking ref here would silently
// bind @<other-ref> to a worktree on a different branch that merely shares a
// commit with origin/<other-ref> (e.g. right after branching).
func TestOverlayWorktreeOnBranchDoesNotMatchCoincidentRemoteRef(t *testing.T) {
	ctx := context.Background()
	container := t.TempDir()

	solution := filepath.Join(container, "github-acme-solution", "main")
	writeWorkspace(t, solution, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host\n")
	require.NoError(t, os.WriteFile(filepath.Join(solution, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    worktree: acme/host@feature\n"), 0o600))

	host := filepath.Join(container, "github-acme-host", "main")
	writeModule(t, host, "kind: module\nname: saas\nservices:\n  - name: gateway\n")
	writeGatewayService(t, host)
	initGitRepo(t, host, "git@github.com:acme/host.git", "main")
	// origin/feature points at the same commit as the checked-out main branch,
	// but the checkout stays on branch main — it has no local or remote identity
	// as "feature". @feature must not bind to it.
	gitRun(t, host, "update-ref", "refs/remotes/origin/feature", "HEAD")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, solution)
	require.NoError(t, err)

	_, err = workspace.LoadModuleFromName(ctx, "saas")
	require.ErrorContains(t, err, "found a checkout of acme/host")
	require.ErrorContains(t, err, "on branch main")
}

func mustModuleRef(t *testing.T, workspace *resources.Workspace, name string) *resources.ModuleReference {
	t.Helper()
	for _, ref := range workspace.Modules {
		if ref.Name == name {
			return ref
		}
	}
	t.Fatalf("module %q not found", name)
	return nil
}

// A service override says where ONE service of a composed module comes from.
// The module itself stays where committed config puts it, and its other
// services stay with it.
func TestOverlayServicePathOverridesOneService(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n")

	saasDir := filepath.Join(dir, "modules", "saas")
	writeModule(t, saasDir, "kind: module\nname: saas\nservices:\n  - name: gateway\n  - name: api\n")
	writeGatewayService(t, saasDir)
	writeAPIService(t, saasDir)

	checkout := filepath.Join(dir, "elsewhere", "gateway")
	writeServiceManifest(t, checkout, serviceManifest("gateway", "go-grpc", "9.9.9", "public-api"))

	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    services:\n      gateway:\n        path: elsewhere/gateway\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	resolution, err := workspace.ResolveModule(ctx, workspace.Modules[0])
	require.NoError(t, err)
	require.Equal(t, resources.ResolutionLocalPath, resolution.Kind)
	require.Equal(t, filepath.Join(dir, "modules", "saas"), resolution.Dir)
	require.Equal(t, resources.ResolutionLocalPath, resolution.Services["gateway"].Kind)
	require.Equal(t, checkout, resolution.Services["gateway"].Dir)

	saas, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)

	gateway, err := saas.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	require.Equal(t, checkout, gateway.Dir())
	// The agent version is exactly what an override is for, so it differs and is
	// not refused.
	require.Equal(t, "9.9.9", gateway.Agent.Version)

	api, err := saas.LoadServiceFromName(ctx, "api")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(saasDir, "services", "api"), api.Dir())
}

// The overlay is the machine-local last word: it outranks the module's own
// committed services[].path, which outranks the services/<name> default.
func TestOverlayServiceOutranksCommittedServicePath(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n")

	saasDir := filepath.Join(dir, "modules", "saas")
	writeModule(t, saasDir, "kind: module\nname: saas\nservices:\n  - name: gateway\n    path: committed\n")
	writeServiceManifest(t, filepath.Join(saasDir, "services", "committed"), serviceManifest("gateway", "go-grpc", "0.0.1", "public-api"))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	saas, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)
	gateway, err := saas.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(saasDir, "services", "committed"), gateway.Dir())

	overlayed := filepath.Join(dir, "elsewhere", "gateway")
	writeServiceManifest(t, overlayed, serviceManifest("gateway", "go-grpc", "0.0.1", "public-api"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    services:\n      gateway:\n        path: elsewhere/gateway\n"), 0o600))

	workspace, err = resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	saas, err = workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)
	gateway, err = saas.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	require.Equal(t, overlayed, gateway.Dir())
}

// An overlay entry carrying only service overrides is valid, and module
// resolution falls through to committed config exactly as if it were absent.
func TestOverlayServicesOnlyEntryLeavesModuleResolutionAlone(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host\n    version: \"1.0\"\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    services:\n      gateway:\n        path: /tmp/gateway\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	resolution, err := workspace.ResolveModule(ctx, workspace.Modules[0])
	require.NoError(t, err)
	require.Equal(t, resources.ResolutionPinned, resolution.Kind)
	require.Equal(t, "acme/host", resolution.Source)
	require.Equal(t, "1.0", resolution.Version)
	require.Equal(t, filepath.Clean("/tmp/gateway"), resolution.Services["gateway"].Dir)
}

// A service override reaches across a worktree boundary the same way a module
// one does: repo@ref names a local checkout, and the service is taken from that
// checkout's copy of the module.
func TestOverlayServiceWorktreeResolvesAgainstRealCheckout(t *testing.T) {
	ctx := context.Background()
	container := t.TempDir()

	solution := filepath.Join(container, "github-acme-solution", "main")
	writeWorkspace(t, solution, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n")
	saasDir := filepath.Join(solution, "modules", "saas")
	writeModule(t, saasDir, "kind: module\nname: saas\nservices:\n  - name: gateway\n")
	writeGatewayService(t, saasDir)
	require.NoError(t, os.WriteFile(filepath.Join(solution, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    services:\n      gateway:\n        worktree: acme/host@feature\n"), 0o600))

	host := filepath.Join(container, "github-acme-host", "feature")
	writeModule(t, host, "kind: module\nname: saas\nservices:\n  - name: gateway\n")
	writeServiceManifest(t, filepath.Join(host, "services", "gateway"), serviceManifest("gateway", "go-grpc", "1.2.3", "public-api"))
	initGitRepo(t, host, "git@github.com:acme/host.git", "feature")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, solution)
	require.NoError(t, err)

	resolution, err := workspace.ResolveModule(ctx, workspace.Modules[0])
	require.NoError(t, err)
	service := resolution.Services["gateway"]
	require.Equal(t, resources.ResolutionWorktree, service.Kind)
	require.Equal(t, "acme/host", service.Source)
	require.Equal(t, "feature", service.Ref)

	saas, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)
	gateway, err := saas.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	realHost, err := filepath.EvalSymlinks(filepath.Join(host, "services", "gateway"))
	require.NoError(t, err)
	resolvedDir, err := filepath.EvalSymlinks(gateway.Dir())
	require.NoError(t, err)
	require.Equal(t, realHost, resolvedDir)
	require.Equal(t, "1.2.3", gateway.Agent.Version)
}

// A version override resolves pinned: core reports the wanted package version
// and does not pull it. Loading such a module is refused, exactly as a pinned
// module is, so the CLI materializes it first.
func TestOverlayServiceVersionResolvesPinnedAndIsNotLoadable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host\n    version: \"1.0\"\n")
	saasDir := filepath.Join(dir, "modules", "saas")
	writeModule(t, saasDir, "kind: module\nname: saas\nservices:\n  - name: gateway\n")
	writeGatewayService(t, saasDir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    path: modules/saas\n    services:\n      gateway:\n        version: \"0.0.66\"\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	resolution, err := workspace.ResolveModule(ctx, workspace.Modules[0])
	require.NoError(t, err)
	service := resolution.Services["gateway"]
	require.Equal(t, resources.ResolutionPinned, service.Kind)
	require.Equal(t, "acme/host", service.Source)
	require.Equal(t, "0.0.66", service.Version)
	require.Empty(t, service.Dir)

	_, err = workspace.LoadModuleFromName(ctx, "saas")
	require.ErrorContains(t, err, "0.0.66")
	require.ErrorContains(t, err, "materialized by the CLI")
}

// A version override needs a source to pull the package from; a module composed
// purely by location has none, and saying so beats resolving to nothing.
func TestOverlayServiceVersionRequiresSource(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    services:\n      gateway:\n        version: \"0.0.66\"\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	_, err = workspace.ResolveModule(ctx, workspace.Modules[0])
	require.ErrorContains(t, err, "without a source")
}

func TestOverlayServiceDirectiveMustSelectExactlyOne(t *testing.T) {
	ctx := context.Background()

	cases := map[string]string{
		"typo'd key":  "resolve:\n  saas:\n    services:\n      gateway:\n        pth: /tmp/x\n",
		"empty entry": "resolve:\n  saas:\n    services:\n      gateway: {}\n",
		"two selected": "resolve:\n  saas:\n    services:\n      gateway:\n        path: /tmp/x\n" +
			"        version: \"0.0.1\"\n",
	}
	for name, overlay := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n    source: acme/host\n    version: \"1.0\"\n")
			require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName), []byte(overlay), 0o600))

			workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
			require.NoError(t, err)

			_, err = workspace.ResolveModule(ctx, workspace.Modules[0])
			require.Error(t, err)
			require.ErrorContains(t, err, "gateway")
			require.ErrorContains(t, err, "saas")
		})
	}
}

// Overriding a service the module does not declare is a typo, not a way to add
// one: the error lists what the module does declare.
func TestOverlayServiceUnknownServiceRejected(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n")
	saasDir := filepath.Join(dir, "modules", "saas")
	writeModule(t, saasDir, "kind: module\nname: saas\nservices:\n  - name: gateway\n  - name: api\n")
	writeGatewayService(t, saasDir)
	writeAPIService(t, saasDir)
	writeServiceManifest(t, filepath.Join(dir, "elsewhere"), serviceManifest("gatway", "go-grpc", "0.0.1"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    services:\n      gatway:\n        path: elsewhere\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	_, err = workspace.LoadModuleFromName(ctx, "saas")
	require.ErrorContains(t, err, "gatway")
	require.ErrorContains(t, err, "gateway, api")
}

// The module's siblings and its interface bind to the composed service's name,
// agent and endpoints, so an override that changes any of them is refused with
// both sides named.
func TestOverlayServiceContractGuard(t *testing.T) {
	ctx := context.Background()

	cases := map[string]struct {
		manifest string
		contains []string
	}{
		"renamed": {
			manifest: serviceManifest("gatekeeper", "go-grpc", "0.0.1", "public-api"),
			contains: []string{"declares name <gatekeeper>", "composes it as <gateway>"},
		},
		"different agent": {
			manifest: serviceManifest("gateway", "python-grpc", "0.0.1", "public-api"),
			contains: []string{"runs agent <python-grpc>", "on agent <go-grpc>"},
		},
		"dropped endpoint": {
			manifest: serviceManifest("gateway", "go-grpc", "0.0.1", "other"),
			contains: []string{"does not expose endpoints public-api", "it exposes other"},
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n")
			saasDir := filepath.Join(dir, "modules", "saas")
			writeModule(t, saasDir, "kind: module\nname: saas\nservices:\n  - name: gateway\n")
			writeGatewayService(t, saasDir)
			writeServiceManifest(t, filepath.Join(dir, "elsewhere"), testCase.manifest)
			require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
				[]byte("resolve:\n  saas:\n    services:\n      gateway:\n        path: elsewhere\n"), 0o600))

			workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
			require.NoError(t, err)

			_, err = workspace.LoadModuleFromName(ctx, "saas")
			require.Error(t, err)
			for _, want := range testCase.contains {
				require.ErrorContains(t, err, want)
			}
		})
	}
}

// Adding an endpoint the module never declared breaks nothing: the guard asks
// for a superset, not an exact match.
func TestOverlayServiceMayAddEndpoints(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: modules\nmodules:\n  - name: saas\n")
	saasDir := filepath.Join(dir, "modules", "saas")
	writeModule(t, saasDir, "kind: module\nname: saas\nservices:\n  - name: gateway\n")
	writeGatewayService(t, saasDir)
	writeServiceManifest(t, filepath.Join(dir, "elsewhere"), serviceManifest("gateway", "go-grpc", "0.0.1", "public-api", "debug"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  saas:\n    services:\n      gateway:\n        path: elsewhere\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	saas, err := workspace.LoadModuleFromName(ctx, "saas")
	require.NoError(t, err)
	gateway, err := saas.LoadServiceFromName(ctx, "gateway")
	require.NoError(t, err)
	require.Len(t, gateway.Endpoints, 2)
}

func TestSaveLocalOverlayRoundTripsServiceOverrides(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	overlay := &resources.LocalOverlay{
		Resolve: map[string]*resources.ModuleResolveDirective{
			"saas": {
				Services: map[string]*resources.ServiceResolveDirective{
					"accounts":  {Path: "/Users/me/module-saas-starter/module/services/accounts"},
					"gateway":   {Worktree: "acme/host@feature"},
					"telemetry": {Version: "0.0.66"},
				},
			},
		},
	}
	require.NoError(t, resources.SaveLocalOverlay(ctx, dir, overlay))

	reloaded, err := resources.LoadLocalOverlay(ctx, dir)
	require.NoError(t, err)
	require.False(t, reloaded.Resolve["saas"].Pinned)
	services := reloaded.Resolve["saas"].Services
	require.Equal(t, "/Users/me/module-saas-starter/module/services/accounts", services["accounts"].Path)
	require.Equal(t, "acme/host@feature", services["gateway"].Worktree)
	require.Equal(t, "0.0.66", services["telemetry"].Version)
}

func writeWorkspace(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "workspace.codefly.yaml"), []byte(content), 0o600))
}

func writeModule(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "module.codefly.yaml"), []byte(content), 0o600))
}

func writeGatewayService(t *testing.T, moduleDir string) {
	t.Helper()
	svcDir := filepath.Join(moduleDir, "services", "gateway")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.codefly.yaml"),
		[]byte("kind: service\nname: gateway\nversion: 0.0.0\nagent:\n  kind: runtime::service\n  name: go-grpc\n  version: 0.0.1\n  publisher: codefly.ai\nendpoints:\n  - name: public-api\n    visibility: public\n"), 0o600))
}

func writeAPIService(t *testing.T, moduleDir string) {
	t.Helper()
	svcDir := filepath.Join(moduleDir, "services", "api")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.codefly.yaml"),
		[]byte("kind: service\nname: api\nversion: 0.0.0\nagent:\n  kind: runtime::service\n  name: go-grpc\n  version: 0.0.1\n  publisher: codefly.ai\nservice-dependencies:\n  - name: gateway\n    module: saas\n    endpoints:\n      - name: public-api\n"), 0o600))
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := testgit.Run(t.Context(), dir, nil, args...)
	require.NoError(t, err, "git %v: %s", args, string(out))
}

func initGitRepo(t *testing.T, dir, remote, branch string) {
	t.Helper()
	gitRun(t, dir, "init", "-b", branch)
	gitRun(t, dir, "remote", "add", "origin", remote)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "initial")
}

// initDetachedRepoAtRemoteRef builds a repo whose HEAD is detached at the commit
// of a remote-tracking ref origin/<ref>, with no local branch named <ref> — the
// state left by `git worktree add <dir> origin/<ref>`.
func initDetachedRepoAtRemoteRef(t *testing.T, dir, remote, ref string) {
	t.Helper()
	initGitRepo(t, dir, remote, "work")
	gitRun(t, dir, "update-ref", "refs/remotes/origin/"+ref, "HEAD")
	gitRun(t, dir, "checkout", "--detach", "HEAD")
}

func writeServiceManifest(t *testing.T, dir, manifest string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "service.codefly.yaml"), []byte(manifest), 0o600))
}

func serviceManifest(name, agent, agentVersion string, endpoints ...string) string {
	manifest := fmt.Sprintf("kind: service\nname: %s\nversion: 0.0.0\nagent:\n  kind: runtime::service\n  name: %s\n  version: %s\n  publisher: codefly.ai\n", name, agent, agentVersion)
	if len(endpoints) > 0 {
		manifest += "endpoints:\n"
		for _, endpoint := range endpoints {
			manifest += fmt.Sprintf("  - name: %s\n    visibility: public\n", endpoint)
		}
	}
	return manifest
}

// A flat (single-module) workspace resolves service overrides too. It used to
// take an early path that skipped them entirely, so `doctor` reported an
// override as active while the run silently loaded the workspace's own copy.
func TestOverlayServiceOverrideAppliesInAFlatWorkspace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: flat\nservices:\n  - name: api\n")
	writeServiceManifest(t, filepath.Join(dir, "services", "api"), serviceManifest("api", "go-grpc", "0.0.1", "public-api"))

	checkout := filepath.Join(dir, "elsewhere")
	writeServiceManifest(t, checkout, serviceManifest("api", "go-grpc", "9.9.9", "public-api"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  solution:\n    services:\n      api:\n        path: elsewhere\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	mod, err := workspace.LoadModuleFromName(ctx, "solution")
	require.NoError(t, err)
	service, err := mod.LoadServiceFromName(ctx, "api")
	require.NoError(t, err)
	require.Equal(t, checkout, service.Dir())
	require.Equal(t, "9.9.9", service.Agent.Version)

	// The committed service list must be untouched: in a flat workspace the
	// module's references are the workspace's own, and writing a machine-local
	// path into them would land in workspace.codefly.yaml on the next save.
	for _, ref := range workspace.Services {
		require.Nil(t, ref.PathOverride, "override leaked into the workspace's committed service list")
	}
}

// The contract guard holds on a flat workspace too: the override still has to
// be the same service the workspace declares.
func TestOverlayServiceOverrideContractGuardAppliesInAFlatWorkspace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: flat\nservices:\n  - name: api\n")
	writeServiceManifest(t, filepath.Join(dir, "services", "api"), serviceManifest("api", "go-grpc", "0.0.1", "public-api"))
	writeServiceManifest(t, filepath.Join(dir, "elsewhere"), serviceManifest("api", "python-grpc", "0.0.1", "public-api"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  solution:\n    services:\n      api:\n        path: elsewhere\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	_, err = workspace.LoadModuleFromName(ctx, "solution")
	require.ErrorContains(t, err, "python-grpc")
	require.ErrorContains(t, err, "go-grpc")
}

// Overriding a service a flat workspace does not declare is rejected with the
// ones it does, exactly as in a composed module.
func TestOverlayServiceOverrideUnknownServiceRejectedInAFlatWorkspace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeWorkspace(t, dir, "name: solution\nlayout: flat\nservices:\n  - name: api\n")
	writeServiceManifest(t, filepath.Join(dir, "services", "api"), serviceManifest("api", "go-grpc", "0.0.1"))
	writeServiceManifest(t, filepath.Join(dir, "elsewhere"), serviceManifest("apu", "go-grpc", "0.0.1"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.LocalOverlayConfigurationName),
		[]byte("resolve:\n  solution:\n    services:\n      apu:\n        path: elsewhere\n"), 0o600))

	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	_, err = workspace.LoadModuleFromName(ctx, "solution")
	require.ErrorContains(t, err, "apu")
	require.ErrorContains(t, err, "api")
}
