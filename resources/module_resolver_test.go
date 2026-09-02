package resources_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
		},
	}
	require.NoError(t, resources.SaveLocalOverlay(ctx, dir, overlay))

	reloaded, err := resources.LoadLocalOverlay(ctx, dir)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	require.Equal(t, "acme/docs@main", reloaded.Resolve["documents"].Worktree)
	require.True(t, reloaded.Resolve["saas"].Pinned)
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
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
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
