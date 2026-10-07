package golang

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/resources"

	"github.com/codefly-dev/core/shared"

	runners "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/runners/companion"

	"github.com/codefly-dev/core/wool"
)

/*
GoRunnerEnvironment is a runner for go
- Init:
  - go modules handling
  - binary building

- Start:
  - start the binary
*/
type GoRunnerEnvironment struct {
	dir       string
	companion companion.CompanionRunner // when using golden wrapper (Docker path)
	local     *runners.NativeEnvironment
	nix       *runners.NixEnvironment

	localCacheDir string

	// CGO or not
	withCGO bool

	// Workspace configuration distinguishes an attached source root's own
	// go.work from an unrelated parent workspace inherited from the host.
	withGoWorkspace   bool
	ownsGoWorkspace   bool
	goWorkspaceFile   string
	workspaceModules  []string
	workspacePackages []string

	withGoModules bool
	goModCache    string

	// Build options

	withDebugSymbol            bool
	withRaceConditionDetection bool

	// targetPath is the executable of the last successful BuildBinary, and
	// empty until one succeeds: a failed build leaves nothing to run.
	targetPath string

	out io.Writer

	// Source directory
	sourceDir string
	moduleDir string
}

func (r *GoRunnerEnvironment) LocalCacheDir(ctx context.Context) string {
	var p string
	switch {
	case r.companion != nil:
		p = path.Join(r.localCacheDir, "container")
	case r.nix != nil:
		p = path.Join(r.localCacheDir, "nix")
	default:
		p = path.Join(r.localCacheDir, "native")
	}
	_, _ = shared.CheckDirectoryOrCreate(ctx, p)
	return p
}

func (r *GoRunnerEnvironment) WithDebugSymbol(debug bool) {
	r.withDebugSymbol = debug
}

func NewNativeGoRunner(ctx context.Context, dir string, relativeSource string) (*GoRunnerEnvironment, error) {
	w := wool.Get(ctx).In("NewNativeGoRunner")
	w.Trace("creating native go runner", wool.Field("dir", dir))

	// Check that go in the path
	_, err := exec.LookPath("go")
	if err != nil {
		return nil, w.NewError("cannot find go in the path")
	}

	local, err := runners.NewNativeEnvironment(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create go local environment")
	}

	sourceDir := path.Join(dir, relativeSource)

	goModDir, withGoModules := findGoModuleDir(ctx, dir, sourceDir)
	workspace, ownsWorkspace, err := loadSourceGoWorkspace(sourceDir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load source-owned Go workspace")
	}
	if ownsWorkspace {
		goModDir = workspace.root
		withGoModules = true
	}

	if !withGoModules {
		w.Warn("running without go modules: not encouraged at all")
	} else {
		if ownsWorkspace {
			w.Trace("found source-owned go.work", wool.DirField(goModDir), wool.Field("modules", len(workspace.moduleDirs)))
		} else {
			w.Trace("found go.mod", wool.DirField(goModDir))
		}
		if v, ok := os.LookupEnv("GOMODCACHE"); ok {
			local.WithEnvironmentVariables(ctx, resources.Env("GOMODCACHE", v))
		} else {
			if v, ok := os.LookupEnv("GOPATH"); ok {
				local.WithEnvironmentVariables(ctx, resources.Env("GOPATH", v))
			}
		}
	}

	// Ensure PATH is inherited so subprocesses (e.g. tests calling exec) can find binaries.
	if p := os.Getenv("PATH"); p != "" {
		local.WithEnvironmentVariables(ctx, resources.Env("PATH", p))
	}

	return &GoRunnerEnvironment{
		dir:               dir,
		local:             local,
		withGoModules:     withGoModules,
		ownsGoWorkspace:   ownsWorkspace,
		goWorkspaceFile:   workspaceFile(workspace),
		workspaceModules:  workspaceModuleDirs(workspace),
		workspacePackages: workspacePackageTargets(workspace),
		localCacheDir:     path.Join(sourceDir, "cache"),
		sourceDir:         sourceDir,
		moduleDir:         goModDir,
	}, nil
}

// NewNixGoRunner creates a Go runner that uses Nix for reproducible builds.
// All tools (go, buf, protoc) come from the flake.nix in dir.
func NewNixGoRunner(ctx context.Context, dir string, relativeSource string) (*GoRunnerEnvironment, error) {
	w := wool.Get(ctx).In("NewNixGoRunner")
	w.Trace("creating nix go runner", wool.Field("dir", dir))

	nixEnv, err := runners.NewNixEnvironment(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create nix environment")
	}

	sourceDir := path.Join(dir, relativeSource)
	goModDir, withGoModules := findGoModuleDir(ctx, dir, sourceDir)
	workspace, ownsWorkspace, err := loadSourceGoWorkspace(sourceDir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load source-owned Go workspace")
	}
	if ownsWorkspace {
		goModDir = workspace.root
		withGoModules = true
	}

	if !withGoModules {
		w.Warn("running without go modules: not encouraged at all")
	} else {
		// Pass through GOMODCACHE/GOPATH from host for caching
		if v, ok := os.LookupEnv("GOMODCACHE"); ok {
			nixEnv.WithEnvironmentVariables(ctx, resources.Env("GOMODCACHE", v))
		} else if v, ok := os.LookupEnv("GOPATH"); ok {
			nixEnv.WithEnvironmentVariables(ctx, resources.Env("GOPATH", v))
		}
	}

	return &GoRunnerEnvironment{
		dir:               dir,
		nix:               nixEnv,
		withGoModules:     withGoModules,
		ownsGoWorkspace:   ownsWorkspace,
		goWorkspaceFile:   workspaceFile(workspace),
		workspaceModules:  workspaceModuleDirs(workspace),
		workspacePackages: workspacePackageTargets(workspace),
		localCacheDir:     path.Join(sourceDir, "cache"),
		sourceDir:         sourceDir,
		moduleDir:         goModDir,
	}, nil
}

func NewDockerGoRunner(ctx context.Context, image *resources.DockerImage, dir string, relativeSource string, name string) (*GoRunnerEnvironment, error) {
	w := wool.Get(ctx).In("NewDockerGoRunner")

	runnerName := fmt.Sprintf("goland-%s", name)
	w.Trace("creating docker go runner", wool.Field("image", image), wool.Field("dir", dir), wool.Field("name", runnerName))

	companion, err := companion.NewCompanionRunner(ctx, companion.CompanionOpts{
		Name:      runnerName,
		SourceDir: dir,
		Image:     image,
	})
	if err != nil {
		return nil, w.Wrapf(err, "cannot create companion runner")
	}
	companion.WithPause()

	sourceDir := path.Join(dir, relativeSource)
	goModDir, withGoModules := findGoModuleDir(ctx, dir, sourceDir)
	workspace, ownsWorkspace, err := loadSourceGoWorkspace(sourceDir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load source-owned Go workspace")
	}
	if ownsWorkspace {
		goModDir = workspace.root
		withGoModules = true
	}
	if !withGoModules {
		w.Warn("running without go modules: not encouraged at all")
	}

	return &GoRunnerEnvironment{
		dir:               dir,
		companion:         companion,
		withGoModules:     withGoModules,
		ownsGoWorkspace:   ownsWorkspace,
		goWorkspaceFile:   workspaceFile(workspace),
		workspaceModules:  workspaceModuleDirs(workspace),
		workspacePackages: workspacePackageTargets(workspace),
		localCacheDir:     path.Join(sourceDir, "cache"),
		sourceDir:         sourceDir,
		moduleDir:         goModDir,
	}, nil
}

func workspaceModuleDirs(workspace *sourceGoWorkspace) []string {
	if workspace == nil {
		return nil
	}
	return append([]string(nil), workspace.moduleDirs...)
}

func workspaceFile(workspace *sourceGoWorkspace) string {
	if workspace == nil {
		return ""
	}
	return workspace.workFile
}

func workspacePackageTargets(workspace *sourceGoWorkspace) []string {
	if workspace == nil {
		return nil
	}
	return append([]string(nil), workspace.packageTargets...)
}

func findGoModuleDir(ctx context.Context, workspaceDir, sourceDir string) (string, bool) {
	// Resolve symlinks once so the workspace-root comparison below
	// doesn't fail on macOS where /var resolves to /private/var.
	// Without this, a sourceDir under /var/folders/... compared to a
	// workspaceDir under /var/folders/... can disagree on equality
	// and the loop walks all the way up to "/" before stopping.
	workspaceRoot := workspaceDir
	if resolved, err := filepath.EvalSymlinks(workspaceDir); err == nil {
		workspaceRoot = resolved
	}
	// Source attachments can point at cmd/tool inside a module outside the
	// ephemeral workspace. Follow that source's real ancestry, as Go does;
	// ordinary sources still stop at their declared workspace boundary.
	if resolved, err := filepath.EvalSymlinks(sourceDir); err == nil {
		if relative, err := filepath.Rel(workspaceRoot, resolved); err == nil &&
			(relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			sourceDir = resolved
		}
	}
	for d := sourceDir; ; d = filepath.Dir(d) {
		exists, _ := shared.FileExists(ctx, filepath.Join(d, "go.mod"))
		if exists {
			return d, true
		}
		stop := d == workspaceDir ||
			d == workspaceRoot ||
			d == string(filepath.Separator) ||
			d == "."
		if !stop {
			if resolved, err := filepath.EvalSymlinks(d); err == nil && resolved == workspaceRoot {
				stop = true
			}
		}
		if stop {
			break
		}
	}
	return sourceDir, false
}

func (r *GoRunnerEnvironment) Env() runners.RunnerEnvironment {
	switch {
	case r.companion != nil:
		return r.companion.RunnerEnv()
	case r.nix != nil:
		return r.nix
	default:
		return r.local
	}
}

func (r *GoRunnerEnvironment) Setup(ctx context.Context) {
	w := wool.Get(ctx).In("setup")
	if !r.withGoModules {
		w.Warn("running without go modules: not encouraged at all")
		r.Env().WithEnvironmentVariables(ctx, resources.Env("GO111MODULE", "off"))
	} else {
		w.Trace("running with go modules")
		r.Env().WithEnvironmentVariables(ctx, resources.Env("GO111MODULE", "on"))
	}
	if r.ownsGoWorkspace {
		// Pin the project-owned workspace explicitly. The agent process may
		// itself run under a developer GOWORK, and source checkouts are often
		// reached through an ephemeral symlink where implicit discovery is not
		// reliable. Project configuration must win over both conditions.
		r.Env().WithEnvironmentVariables(ctx, resources.Env("GOWORK", r.goWorkspaceFile))
	}
	if r.companion != nil {
		r.companion.WithMount(r.LocalCacheDir(ctx), "/build")
		if r.goModCache != "" {
			w.Focus("using go mod cache", wool.Field("dir", r.goModCache))
			_, err := shared.CheckDirectoryOrCreate(ctx, r.goModCache)
			if err != nil {
				w.Warn("cannot create go mod cache", wool.ErrField(err))
			}
			r.companion.WithMount(r.goModCache, "/go/pkg/mod")
			return
		}
		if v, ok := os.LookupEnv("GOMODCACHE"); ok {
			w.Focus("using go mod cache", wool.Field("dir", v))
			_, err := shared.CheckDirectoryOrCreate(ctx, v)
			if err != nil {
				wool.Get(ctx).Warn("cannot create go mod cache", wool.ErrField(err))
			}
			r.companion.WithMount(v, "/go/pkg/mod")
		} else if v, ok := os.LookupEnv("GOPATH"); ok {
			dir := path.Join(v, "pkg/mod")
			_, err := shared.CheckDirectoryOrCreate(ctx, dir)
			if err != nil {
				wool.Get(ctx).Warn("cannot create go mod cache", wool.ErrField(err))
			}
			r.companion.WithMount(dir, "/go/pkg/mod")
		} else {
			goModCache := path.Join(resources.CodeflyDir(), "go/pkg/mod")
			_, err := shared.CheckDirectoryOrCreate(ctx, goModCache)
			if err != nil {
				wool.Get(ctx).Warn("cannot create go mod cache", wool.ErrField(err))
			}
			r.companion.WithMount(goModCache, "/go/pkg/mod")
		}
	}
	if r.nix != nil {
		// Nix: pass through HOME for go cache, goModCache if set
		if r.goModCache != "" {
			r.Env().WithEnvironmentVariables(ctx, resources.Env("GOMODCACHE", r.goModCache))
		}
		r.Env().WithEnvironmentVariables(ctx, resources.Env("HOME", os.Getenv("HOME")))
	}
	if r.local != nil {
		if r.goModCache != "" {
			r.Env().WithEnvironmentVariables(ctx, resources.Env("GOMODCACHE", r.goModCache))
		}
		r.Env().WithEnvironmentVariables(ctx, resources.Env("HOME", os.Getenv("HOME")))
	}
}

func (r *GoRunnerEnvironment) WithOutput(out io.Writer) {
	r.out = out
}

func (r *GoRunnerEnvironment) WithCGO(b bool) {
	r.withCGO = b
}

func (r *GoRunnerEnvironment) WithWorkspace(b bool) {
	// A go.work at the attached source root is part of the project contract.
	// The setting controls inheritance from ambient parent workspaces only.
	r.withGoWorkspace = b || r.ownsGoWorkspace
}

func (r *GoRunnerEnvironment) Init(ctx context.Context) error {
	w := wool.Get(ctx).In("init")

	r.Setup(ctx)
	if r.companion != nil {
		if err := r.companion.Init(ctx); err != nil {
			return w.Wrapf(err, "cannot init companion")
		}
	} else if err := r.Env().Init(ctx); err != nil {
		return w.Wrapf(err, "cannot init environment")
	}

	// ARCHITECTURE: lifecycle initialization is shared by read-only Code and
	// Tooling RPCs. Resolving modules here can create go.sum in the attached
	// user checkout during inspection. Execution entry points prepare their
	// dependencies lazily immediately before build, test, or lint instead.
	return nil
}

func (r *GoRunnerEnvironment) GoModuleHandling(ctx context.Context) error {
	w := wool.Get(ctx).In("goModuleHandling")
	moduleDir := r.moduleDir
	if moduleDir == "" {
		moduleDir = r.sourceDir
	}
	components := []*builders.Dependency{builders.NewDependency("go.mod", "go.sum").Localize(moduleDir)}
	moduleDirs := []string{moduleDir}
	if r.ownsGoWorkspace {
		components = []*builders.Dependency{builders.NewDependency("go.work", "go.work.sum").Localize(r.sourceDir)}
		moduleDirs = append([]string(nil), r.workspaceModules...)
		for _, dir := range moduleDirs {
			components = append(components, builders.NewDependency("go.mod", "go.sum").Localize(dir))
		}
	}
	req := builders.NewDependencies("gomod", components...)
	req.WithCache(r.LocalCacheDir(ctx))

	updated, err := req.Updated(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot check go mod")
	}

	if !updated {
		w.Trace("go modules have been cached")
		return nil
	}
	for _, dir := range moduleDirs {
		proc, err := r.Env().NewProcess("go", "mod", "download")
		if err != nil {
			return w.Wrapf(err, "cannot create go mod download process for %s", dir)
		}
		var output bytes.Buffer
		writer := io.Writer(&output)
		if r.out != nil {
			writer = io.MultiWriter(r.out, &output)
		}
		proc.WithOutput(writer)
		proc.WithDir(dir)
		// Ambient parent workspaces are disabled; a workspace owned by the
		// attached source root remains authoritative for all module work.
		if !r.withGoWorkspace {
			proc.WithEnvironmentVariables(ctx, resources.Env("GOWORK", "off"))
		}
		if err := proc.Run(ctx); err != nil {
			if detail := strings.TrimSpace(output.String()); detail != "" {
				return w.Wrapf(err, "cannot run go mod download in %s: %s", dir, detail)
			}
			return w.Wrapf(err, "cannot run go mod download in %s", dir)
		}
	}

	if err = req.UpdateCache(ctx); err != nil {
		return w.Wrapf(err, "cannot update go mod cache")
	}
	return nil
}

// binaryName is the one executable the runner keeps per build environment.
// Its name is fixed on purpose: `go build -o` reads the build ID the existing
// executable carries and relinks only when the action graph no longer
// produces it. A name derived from a key of the runner's own would be that
// key again.
const binaryName = "main"

// binaryPath is the executable's path as the build environment sees it. A
// Docker companion mounts the host cache directory at /build; native and Nix
// builds write the host path.
func (r *GoRunnerEnvironment) binaryPath(ctx context.Context) string {
	if r.companion != nil && r.companion.Backend() == companion.BackendDocker {
		return path.Join("/build", binaryName)
	}
	return path.Join(r.LocalCacheDir(ctx), binaryName)
}

// BuildBinary runs `go build -o` on the runner's executable, every time. The
// Go toolchain owns reuse: it identifies the complete action graph — every
// compiled input (sources, embedded files, cgo and assembly inputs, transitive
// and locally replaced packages) and the link action with its linker flags —
// and leaves the executable in place when the build ID it carries is the one
// that graph produces. Nothing changed costs one `go build` that does nothing.
//
// The runner keeps no key of its own. One built from compiled package build
// IDs plus a source hash served a stale executable when only `-ldflags`
// changed under `-trimpath`: Go records no linker flags in package metadata
// there (go.dev/issue/52372), so no package ID moved while the link action
// did. Enumerating more flags into such a key is the weaker form of this fix.
//
// A build that did not complete leaves nothing to run. targetPath is cleared
// before the first fallible step and set again only once `go build` has
// exited 0 — the real exit, since runners/base reports a signal as the
// failure it is, and a real build, since -n=false pins executing mode. Module
// preparation failing, a dry run and an interrupted build all leave Runner
// refusing rather than serving the previous executable. There is no check
// that the file exists after the exit: `go build -o` reuses the executable in
// place, so after any earlier success the file is always there, and such a
// check would only have protected the very first build.
func (r *GoRunnerEnvironment) BuildBinary(ctx context.Context) error {
	w := wool.Get(ctx).In("buildBinary")
	r.targetPath = ""
	if r.withGoModules {
		if err := r.GoModuleHandling(ctx); err != nil {
			return w.Wrapf(err, "cannot handle go modules")
		}
	}
	target := r.binaryPath(ctx)
	w.Trace("building binary", wool.FileField(target))

	// -n=false pins executing mode: GOFLAGS=-n, inherited or injected, would
	// make this a dry run that prints its plan, exits 0 and writes nothing.
	// An explicit flag on the command line overrides GOFLAGS in Go's parser.
	args := append([]string{"build", "-n=false"}, r.buildFlags()...)
	args = append(args, "-o", target)

	proc, err := r.Env().NewProcess("go", args...)
	if err != nil {
		return w.Wrapf(err, "cannot create go build process")
	}
	if r.out != nil {
		proc.WithOutput(r.out)
	}
	if err := r.configureBuildProcess(ctx, proc); err != nil {
		return err
	}
	if err := proc.Run(ctx); err != nil {
		return w.Wrapf(err, "cannot run go build")
	}
	r.targetPath = target
	return nil
}

func (r *GoRunnerEnvironment) buildFlags() []string {
	var flags []string
	if r.withDebugSymbol {
		flags = append(flags, "-gcflags", "all=-N -l")
	}
	if r.withRaceConditionDetection {
		flags = append(flags, "-race")
	}
	return flags
}

func (r *GoRunnerEnvironment) configureBuildProcess(ctx context.Context, proc runners.Proc) error {
	proc.WithDir(r.sourceDir)
	if r.withCGO {
		// A native build uses the host toolchain, so fail early when its C
		// compiler is missing. Docker and Nix builds must resolve cc inside
		// their own environment; injecting the host PATH hides the devShell
		// tools (including go itself) and breaks reproducibility.
		if r.local != nil {
			if _, err := exec.LookPath("cc"); err != nil {
				return fmt.Errorf("cannot find cc: %w", err)
			}
		}
		proc.WithEnvironmentVariables(ctx, resources.Env("CC", "cc"))
		proc.WithEnvironmentVariables(ctx, resources.Env("CGO_ENABLED", "1"))
	}
	if !r.withGoWorkspace {
		proc.WithEnvironmentVariables(ctx, resources.Env("GOWORK", "off"))
	}

	return nil
}

func (r *GoRunnerEnvironment) Stop(ctx context.Context) error {
	return r.Env().Stop(ctx)
}

func (r *GoRunnerEnvironment) Shutdown(ctx context.Context) error {
	return r.Env().Shutdown(ctx)
}

func (r *GoRunnerEnvironment) WithGoModDir(dir string) {
	r.goModCache = dir
}

func (r *GoRunnerEnvironment) WithLocalCacheDir(dir string) {
	r.localCacheDir = dir
}

// Runner is a process for the executable of the last successful BuildBinary.
func (r *GoRunnerEnvironment) Runner(args ...string) (runners.Proc, error) {
	if r.targetPath == "" {
		return nil, fmt.Errorf("no executable to run: BuildBinary has not succeeded")
	}
	proc, err := r.Env().NewProcess(r.targetPath, args...)
	if err != nil {
		return nil, err
	}
	proc.WithDir(r.sourceDir)
	return proc, nil
}

func (r *GoRunnerEnvironment) WithRaceConditionDetection(b bool) {
	r.withRaceConditionDetection = b

}

func (r *GoRunnerEnvironment) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	r.Env().WithEnvironmentVariables(ctx, envs...)
}

func (r *GoRunnerEnvironment) WithFile(file string, location string) {
	if r.companion != nil {
		r.companion.WithMount(file, location)
	}
}

func (r *GoRunnerEnvironment) WithPort(ctx context.Context, port uint32) {
	if r.companion != nil {
		r.companion.WithPortMapping(ctx, uint16(port), uint16(port))
	}
}
