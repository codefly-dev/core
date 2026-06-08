package rust

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/resources"

	"github.com/codefly-dev/core/shared"

	runners "github.com/codefly-dev/core/runners/base"

	"github.com/codefly-dev/core/wool"
)

/*
RustRunnerEnvironment is a runner for Rust/Cargo.
- Init:
  - cargo dependency handling (cargo fetch)
  - binary building (cargo build)

- Start:
  - start the compiled binary

It mirrors golang.GoRunnerEnvironment and reuses the same base execution
environments (native, Docker, Nix).
*/
type RustRunnerEnvironment struct {
	dir       string
	companion runners.CompanionRunner // when using the Docker path
	local     *runners.NativeEnvironment
	nix       *runners.NixEnvironment

	localCacheDir string

	// Used to cache the binary
	requirements *builders.Dependencies

	// Build profile
	release         bool
	withDebugSymbol bool

	// CARGO_HOME registry cache (host path) mounted into the container.
	cargoHome string

	out io.Writer

	// Resolved binary name (Cargo package / [[bin]] name).
	binaryName string

	targetPath string

	// For testing mostly
	usedCache bool

	// Source directory and the directory holding Cargo.toml.
	sourceDir string
	moduleDir string
}

func (r *RustRunnerEnvironment) LocalCacheDir(ctx context.Context) string {
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

func (r *RustRunnerEnvironment) WithDebugSymbol(debug bool) {
	r.withDebugSymbol = debug
}

// WithRelease toggles the optimized `--release` profile.
func (r *RustRunnerEnvironment) WithRelease(b bool) {
	r.release = b
}

func NewNativeRustRunner(ctx context.Context, dir string, relativeSource string) (*RustRunnerEnvironment, error) {
	w := wool.Get(ctx).In("NewNativeRustRunner")
	w.Trace("creating native rust runner", wool.Field("dir", dir))

	// Check that cargo is in the path
	if _, err := exec.LookPath("cargo"); err != nil {
		return nil, w.NewError("cannot find cargo in the path")
	}

	local, err := runners.NewNativeEnvironment(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create rust local environment")
	}

	sourceDir := path.Join(dir, relativeSource)
	moduleDir := findCargoManifestDir(ctx, dir, sourceDir)

	if v, ok := os.LookupEnv("CARGO_HOME"); ok {
		local.WithEnvironmentVariables(ctx, resources.Env("CARGO_HOME", v))
	}
	// Ensure PATH is inherited so subprocesses (e.g. tests calling exec) can find binaries.
	if p := os.Getenv("PATH"); p != "" {
		local.WithEnvironmentVariables(ctx, resources.Env("PATH", p))
	}

	return &RustRunnerEnvironment{
		dir:           dir,
		local:         local,
		localCacheDir: path.Join(sourceDir, "cache"),
		sourceDir:     sourceDir,
		moduleDir:     moduleDir,
	}, nil
}

// NewNixRustRunner creates a Rust runner that uses Nix for reproducible
// builds. All tools (cargo, rustc) come from the flake.nix in dir.
func NewNixRustRunner(ctx context.Context, dir string, relativeSource string) (*RustRunnerEnvironment, error) {
	w := wool.Get(ctx).In("NewNixRustRunner")
	w.Trace("creating nix rust runner", wool.Field("dir", dir))

	nixEnv, err := runners.NewNixEnvironment(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create nix environment")
	}

	sourceDir := path.Join(dir, relativeSource)
	moduleDir := findCargoManifestDir(ctx, dir, sourceDir)

	if v, ok := os.LookupEnv("CARGO_HOME"); ok {
		nixEnv.WithEnvironmentVariables(ctx, resources.Env("CARGO_HOME", v))
	}

	return &RustRunnerEnvironment{
		dir:           dir,
		nix:           nixEnv,
		localCacheDir: path.Join(sourceDir, "cache"),
		sourceDir:     sourceDir,
		moduleDir:     moduleDir,
	}, nil
}

func NewDockerRustRunner(ctx context.Context, image *resources.DockerImage, dir string, relativeSource string, name string) (*RustRunnerEnvironment, error) {
	w := wool.Get(ctx).In("NewDockerRustRunner")

	runnerName := fmt.Sprintf("rust-%s", name)
	w.Trace("creating docker rust runner", wool.Field("image", image), wool.Field("dir", dir), wool.Field("name", runnerName))

	companion, err := runners.NewCompanionRunner(ctx, runners.CompanionOpts{
		Name:      runnerName,
		SourceDir: dir,
		Image:     image,
	})
	if err != nil {
		return nil, w.Wrapf(err, "cannot create companion runner")
	}
	companion.WithPause()

	sourceDir := path.Join(dir, relativeSource)
	moduleDir := findCargoManifestDir(ctx, dir, sourceDir)

	return &RustRunnerEnvironment{
		dir:           dir,
		companion:     companion,
		localCacheDir: path.Join(sourceDir, "cache"),
		sourceDir:     sourceDir,
		moduleDir:     moduleDir,
	}, nil
}

// findCargoManifestDir walks up from sourceDir to find the directory that
// holds Cargo.toml, stopping at the workspace root. Mirrors
// golang.findGoModuleDir. Falls back to sourceDir when none is found.
func findCargoManifestDir(ctx context.Context, workspaceDir, sourceDir string) string {
	workspaceRoot := workspaceDir
	if resolved, err := filepath.EvalSymlinks(workspaceDir); err == nil {
		workspaceRoot = resolved
	}
	for d := sourceDir; ; d = filepath.Dir(d) {
		exists, _ := shared.FileExists(ctx, filepath.Join(d, "Cargo.toml"))
		if exists {
			return d
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
	return sourceDir
}

func (r *RustRunnerEnvironment) Env() runners.RunnerEnvironment {
	switch {
	case r.companion != nil:
		return r.companion.RunnerEnv()
	case r.nix != nil:
		return r.nix
	default:
		return r.local
	}
}

// manifestDir returns the directory holding Cargo.toml (the working
// directory for all cargo invocations).
func (r *RustRunnerEnvironment) manifestDir() string {
	if r.moduleDir != "" {
		return r.moduleDir
	}
	return r.sourceDir
}

func (r *RustRunnerEnvironment) Setup(ctx context.Context) {
	w := wool.Get(ctx).In("setup")
	if r.companion != nil {
		r.companion.WithMount(r.LocalCacheDir(ctx), "/build")
		// Mount the host cargo registry so dependency downloads are cached
		// across runs. The rust image sets CARGO_HOME=/usr/local/cargo.
		registry := r.cargoHome
		if registry == "" {
			if v, ok := os.LookupEnv("CARGO_HOME"); ok {
				registry = v
			} else {
				registry = path.Join(resources.CodeflyDir(), "cargo")
			}
		}
		regDir := path.Join(registry, "registry")
		if _, err := shared.CheckDirectoryOrCreate(ctx, regDir); err != nil {
			w.Warn("cannot create cargo registry cache", wool.ErrField(err))
		} else {
			r.companion.WithMount(regDir, "/usr/local/cargo/registry")
		}
		return
	}
	// native / nix: pass through CARGO_HOME + HOME for the cargo cache.
	if r.cargoHome != "" {
		r.Env().WithEnvironmentVariables(ctx, resources.Env("CARGO_HOME", r.cargoHome))
	}
	r.Env().WithEnvironmentVariables(ctx, resources.Env("HOME", os.Getenv("HOME")))
}

func (r *RustRunnerEnvironment) WithOutput(out io.Writer) {
	r.out = out
}

func (r *RustRunnerEnvironment) Init(ctx context.Context) error {
	w := wool.Get(ctx).In("init")

	r.Setup(ctx)
	if r.companion != nil {
		if err := r.companion.Init(ctx); err != nil {
			return w.Wrapf(err, "cannot init companion")
		}
	} else if err := r.Env().Init(ctx); err != nil {
		return w.Wrapf(err, "cannot init environment")
	}

	if err := r.CargoDependencyHandling(ctx); err != nil {
		return w.Wrapf(err, "cannot handle cargo dependencies")
	}
	return nil
}

// CargoDependencyHandling runs `cargo fetch` when Cargo.toml/Cargo.lock
// have changed since the last cached run. Mirrors GoModuleHandling.
func (r *RustRunnerEnvironment) CargoDependencyHandling(ctx context.Context) error {
	w := wool.Get(ctx).In("cargoDependencyHandling")
	moduleDir := r.manifestDir()

	req := builders.NewDependencies("cargo", builders.NewDependency("Cargo.toml", "Cargo.lock").Localize(moduleDir))
	req.WithCache(r.LocalCacheDir(ctx))

	updated, err := req.Updated(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot check cargo manifest")
	}
	if !updated {
		w.Trace("cargo dependencies have been cached")
		return nil
	}
	if err = req.UpdateCache(ctx); err != nil {
		return w.Wrapf(err, "cannot update cargo cache")
	}

	proc, err := r.Env().NewProcess("cargo", "fetch")
	if err != nil {
		return w.Wrapf(err, "cannot create cargo fetch process")
	}
	if r.out != nil {
		proc.WithOutput(r.out)
	}
	proc.WithDir(moduleDir)
	if err = proc.Run(ctx); err != nil {
		return w.Wrapf(err, "cannot run cargo fetch")
	}
	return nil
}

// profile returns the Cargo build profile directory name.
func (r *RustRunnerEnvironment) profile() string {
	if r.release {
		return "release"
	}
	return "debug"
}

func (r *RustRunnerEnvironment) BinName(hash string) string {
	if r.withDebugSymbol {
		return fmt.Sprintf("%s-debug", hash)
	}
	return hash
}

func (r *RustRunnerEnvironment) LocalTargetPath(ctx context.Context, hash string) string {
	return path.Join(r.LocalCacheDir(ctx), r.BinName(hash))
}

// targetDir is where cargo writes build artifacts (`--target-dir`). In the
// Docker path this is /build (mounted to LocalCacheDir); otherwise it is a
// "target" subdirectory of the local cache.
func (r *RustRunnerEnvironment) buildTargetDir(ctx context.Context) string {
	if r.companion != nil && r.companion.Backend() == runners.BackendDocker {
		return "/build/target"
	}
	return path.Join(r.LocalCacheDir(ctx), "target")
}

// hostArtifactPath is the host-side location of the compiled binary produced
// by cargo into buildTargetDir. For Docker, /build maps to LocalCacheDir.
func (r *RustRunnerEnvironment) hostArtifactPath(ctx context.Context) string {
	if r.companion != nil && r.companion.Backend() == runners.BackendDocker {
		return path.Join(r.LocalCacheDir(ctx), "target", r.profile(), r.binaryName)
	}
	return path.Join(r.buildTargetDir(ctx), r.profile(), r.binaryName)
}

func (r *RustRunnerEnvironment) BuildBinary(ctx context.Context) error {
	w := wool.Get(ctx).In("buildBinary")

	hashDir := r.manifestDir()
	r.requirements = builders.NewDependencies("rust",
		builders.NewDependency(hashDir).WithPathSelect(shared.NewSelect("*.rs")),
		builders.NewDependency("Cargo.toml", "Cargo.lock").Localize(hashDir),
	)

	w.Trace("start building")
	r.usedCache = false

	hash, err := r.requirements.Hash(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot get hash")
	}

	name, err := r.resolveBinaryName()
	if err != nil {
		return w.Wrapf(err, "cannot resolve cargo binary name")
	}
	r.binaryName = name

	cached := r.LocalTargetPath(ctx, hash)
	w.Trace("checking local cache", wool.FileField(cached))
	exists, err := shared.FileExists(ctx, cached)
	if err != nil {
		return w.Wrapf(err, "cannot check local cache")
	}
	if exists {
		w.Trace("found a cache binary: don't work until we have to!", wool.FileField(cached))
		r.usedCache = true
		r.targetPath = cached
		return nil
	}

	args := []string{"build", "--target-dir", r.buildTargetDir(ctx)}
	if r.release {
		args = append(args, "--release")
	}

	proc, err := r.Env().NewProcess("cargo", args...)
	if err != nil {
		return w.Wrapf(err, "cannot create cargo build process")
	}
	if r.out != nil {
		proc.WithOutput(r.out)
	}
	proc.WithDir(r.manifestDir())
	if r.withDebugSymbol {
		proc.WithEnvironmentVariables(ctx, resources.Env("RUSTFLAGS", "-C debuginfo=2"))
	}

	if err = proc.Run(ctx); err != nil {
		return w.Wrapf(err, "cannot run cargo build")
	}

	// Cargo writes target/<profile>/<binary>. Copy it into the hashed cache
	// slot so subsequent identical builds short-circuit (mirrors `go build -o`).
	produced := r.hostArtifactPath(ctx)
	if err = copyExecutable(produced, cached); err != nil {
		return w.Wrapf(err, "cannot cache built binary from %s", produced)
	}
	r.targetPath = cached
	return nil
}

// resolveBinaryName returns the binary name cargo will produce: the [[bin]]
// name if present, otherwise the [package] name from Cargo.toml.
func (r *RustRunnerEnvironment) resolveBinaryName() (string, error) {
	manifest := filepath.Join(r.manifestDir(), "Cargo.toml")
	data, err := os.ReadFile(manifest)
	if err != nil {
		return "", err
	}
	if name := parseCargoBinaryName(string(data)); name != "" {
		return name, nil
	}
	return "", fmt.Errorf("no [package] name found in %s", manifest)
}

var cargoNameRe = regexp.MustCompile(`(?m)^\s*name\s*=\s*"([^"]+)"`)

// parseCargoBinaryName extracts the binary name from Cargo.toml contents.
// Prefers the first [[bin]] name, falling back to the [package] name. This
// is a deliberately small TOML scan — Cargo manifests put name on its own
// line — avoiding a full TOML-parser dependency.
func parseCargoBinaryName(toml string) string {
	var pkgName, binName string
	section := ""
	for _, line := range strings.Split(toml, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			section = trim
			continue
		}
		m := cargoNameRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		switch {
		case section == "[package]" && pkgName == "":
			pkgName = m[1]
		case section == "[[bin]]" && binName == "":
			binName = m[1]
		}
	}
	if binName != "" {
		return binName
	}
	return pkgName
}

// copyExecutable copies src to dst with executable permissions, creating the
// parent directory. Used to seed the hashed binary cache.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func (r *RustRunnerEnvironment) Stop(ctx context.Context) error {
	return r.Env().Stop(ctx)
}

func (r *RustRunnerEnvironment) Shutdown(ctx context.Context) error {
	return r.Env().Shutdown(ctx)
}

func (r *RustRunnerEnvironment) WithCargoHome(dir string) {
	r.cargoHome = dir
}

func (r *RustRunnerEnvironment) WithLocalCacheDir(dir string) {
	r.localCacheDir = dir
}

func (r *RustRunnerEnvironment) Runner(args ...string) (runners.Proc, error) {
	proc, err := r.Env().NewProcess(r.targetPath, args...)
	if err != nil {
		return nil, err
	}
	proc.WithDir(r.sourceDir)
	return proc, nil
}

func (r *RustRunnerEnvironment) UsedCache() bool {
	return r.usedCache
}

func (r *RustRunnerEnvironment) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	r.Env().WithEnvironmentVariables(ctx, envs...)
}

func (r *RustRunnerEnvironment) WithFile(file string, location string) {
	if r.companion != nil {
		r.companion.WithMount(file, location)
	}
}

func (r *RustRunnerEnvironment) WithPort(ctx context.Context, port uint32) {
	if r.companion != nil {
		r.companion.WithPortMapping(ctx, uint16(port), uint16(port))
	}
}
