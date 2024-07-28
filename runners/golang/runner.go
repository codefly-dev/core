package golang

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/resources"

	"github.com/codefly-dev/core/shared"

	runners "github.com/codefly-dev/core/runners/base"

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
	// Possible environments
	dir    string
	docker *runners.DockerEnvironment
	local  *runners.NativeEnvironment

	localCacheDir string

	// Used to cache the binary
	requirements *builders.Dependencies

	withGoModules bool
	goModCache    string

	// Build options

	withDebugSymbol            bool
	withRaceConditionDetection bool

	targetPath string

	// For testing mostly
	usedCache bool

	out io.Writer

	// Source directory
	sourceDir string
}

func (r *GoRunnerEnvironment) LocalCacheDir(ctx context.Context) string {
	var p string
	if r.docker != nil {
		p = path.Join(r.localCacheDir, "container")
	} else {
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
	w.Debug("creating native go runner", wool.Field("dir", dir))

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
	withGoModules, err := shared.FileExists(ctx, path.Join(sourceDir, "go.mod"))
	if err != nil {
		return nil, w.Wrapf(err, "cannot check go modules")
	}

	if !withGoModules {
		w.Warn("running without go modules: not encouraged at all")
	} else {
		// Setup up the proper environment
		if v, ok := os.LookupEnv("GOMODCACHE"); ok {
			local.WithEnvironmentVariables(ctx, resources.Env("GOMODCACHE", v))
		} else {
			if v, ok := os.LookupEnv("GOPATH"); ok {
				local.WithEnvironmentVariables(ctx, resources.Env("GOPATH", v))
			}
		}
	}

	return &GoRunnerEnvironment{
		dir:           dir,
		local:         local,
		withGoModules: withGoModules,
		localCacheDir: path.Join(sourceDir, "cache"),
		sourceDir:     sourceDir,
	}, nil
}

func NewDockerGoRunner(ctx context.Context, image *resources.DockerImage, dir string, relativeSource string, name string) (*GoRunnerEnvironment, error) {
	w := wool.Get(ctx).In("NewDockerGoRunner")

	name = fmt.Sprintf("goland-%s", name)

	w.Debug("creating docker go runner", wool.Field("image", image), wool.Field("dir", dir), wool.Field("name", name))

	docker, err := runners.NewDockerEnvironment(ctx, image, dir, name)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create go docker environment")
	}

	docker.WithPause()

	sourceDir := path.Join(dir, relativeSource)

	withGoModules, err := shared.FileExists(ctx, path.Join(sourceDir, "go.mod"))
	if err != nil {
		return nil, w.Wrapf(err, "cannot check go modules")
	}

	if !withGoModules {
		w.Warn("running without go modules: not encouraged at all")
	}

	return &GoRunnerEnvironment{
		dir:           dir,
		docker:        docker,
		withGoModules: withGoModules,
		localCacheDir: path.Join(sourceDir, "cache"),
		sourceDir:     sourceDir,
	}, nil
}

func (r *GoRunnerEnvironment) Env() runners.RunnerEnvironment {
	if r.docker != nil {
		return r.docker
	}
	return r.local
}

func (r *GoRunnerEnvironment) Setup(ctx context.Context) {
	w := wool.Get(ctx).In("setup")
	if !r.withGoModules {
		w.Warn("running without go modules: not encouraged at all")
		r.Env().WithEnvironmentVariables(ctx, resources.Env("GO111MODULE", "off"))
	} else {
		w.Debug("running with go modules")
		r.Env().WithEnvironmentVariables(ctx, resources.Env("GO111MODULE", "on"))
	}
	if r.docker != nil {
		// Build
		r.docker.WithMount(r.LocalCacheDir(ctx), "/build")
		if r.goModCache != "" {
			w.Focus("using go mod cache", wool.Field("dir", r.goModCache))
			_, err := shared.CheckDirectoryOrCreate(ctx, r.goModCache)
			if err != nil {
				w.Warn("cannot create go mod cache", wool.ErrField(err))
			}
			r.docker.WithMount(r.goModCache, "/go/pkg/mod")
			return
		}
		// Setup up the proper environment
		if v, ok := os.LookupEnv("GOMODCACHE"); ok {
			w.Focus("using go mod cache", wool.Field("dir", v))
			// Mount
			_, err := shared.CheckDirectoryOrCreate(ctx, v)
			if err != nil {
				wool.Get(ctx).Warn("cannot create go mod cache", wool.ErrField(err))
			}
			r.docker.WithMount(v, "/go/pkg/mod")
		} else if v, ok := os.LookupEnv("GOPATH"); ok {
			// Mount
			dir := path.Join(v, "pkg/mod")
			_, err := shared.CheckDirectoryOrCreate(ctx, dir)
			if err != nil {
				wool.Get(ctx).Warn("cannot create go mod cache", wool.ErrField(err))
			}
			r.docker.WithMount(dir, "/go/pkg/mod")
		} else {
			// Use codefly configuration path
			goModCache := path.Join(resources.CodeflyDir(), "go/pkg/mod")
			_, err := shared.CheckDirectoryOrCreate(ctx, goModCache)
			if err != nil {
				wool.Get(ctx).Warn("cannot create go mod cache", wool.ErrField(err))
			}
			r.docker.WithMount(goModCache, "/go/pkg/mod")
		}
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

func (r *GoRunnerEnvironment) Init(ctx context.Context) error {
	w := wool.Get(ctx).In("init")

	r.Setup(ctx)

	err := r.Env().Init(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot init environment")
	}

	if r.withGoModules {
		err = r.GoModuleHandling(ctx)
		if err != nil {
			return w.Wrapf(err, "cannot handle go modules")
		}
	}
	return nil
}

func (r *GoRunnerEnvironment) GoModuleHandling(ctx context.Context) error {
	w := wool.Get(ctx).In("goModuleHandling")
	req := builders.NewDependencies("gomod", builders.NewDependency("go.mod", "go.sum").Localize(r.sourceDir))
	req.WithCache(r.LocalCacheDir(ctx))

	updated, err := req.Updated(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot check go mod")
	}

	if !updated {
		w.Debug("go modules have been cached")
		return nil
	}
	err = req.UpdateCache(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot update go mod cache")
	}

	proc, err := r.Env().NewProcess("go", "mod", "download")
	if err != nil {
		return w.Wrapf(err, "cannot go mod download process")
	}

	if r.out != nil {
		proc.WithOutput(r.out)
	}
	proc.WithDir(r.sourceDir)

	err = proc.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot run go mod download")
	}
	return nil
}

func (r *GoRunnerEnvironment) BinName(hash string) string {
	if r.withDebugSymbol {
		return fmt.Sprintf("%s-debug", hash)
	}
	return hash
}

func (r *GoRunnerEnvironment) LocalTargetPath(ctx context.Context, hash string) string {
	return path.Join(r.LocalCacheDir(ctx), r.BinName(hash))
}

func (r *GoRunnerEnvironment) BuildTargetPath(ctx context.Context, hash string) string {
	if r.docker != nil {
		return path.Join("/build", r.BinName(hash))
	}
	return path.Join(r.LocalCacheDir(ctx), r.BinName(hash))
}

func (r *GoRunnerEnvironment) BuildBinary(ctx context.Context) error {
	w := wool.Get(ctx).In("buildBinary")

	// Setup the requirements
	r.requirements = builders.NewDependencies("go",
		builders.NewDependency(r.sourceDir).WithPathSelect(shared.NewSelect("*.go")))

	w.Debug("start building")
	r.usedCache = false

	hash, err := r.requirements.Hash(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot get hash")
	}

	r.targetPath = r.BuildTargetPath(ctx, hash)

	cached := r.LocalTargetPath(ctx, hash)
	w.Debug("checking local cache", wool.FileField(cached))

	exists, err := shared.FileExists(ctx, cached)
	if err != nil {
		return w.Wrapf(err, "cannot check local cache")
	}
	if exists {
		w.Debug("found a cache binary: don't work until we have to!", wool.FileField(cached))
		r.usedCache = true
		return nil
	}
	// clean cache
	err = shared.EmptyDir(ctx, r.LocalCacheDir(ctx))
	if err != nil {
		return w.Wrapf(err, "cannot clean cache")
	}
	w.Debug("building binary", wool.FileField(r.targetPath))

	args := []string{"build"}
	if r.withDebugSymbol {
		args = append(args, "-gcflags", "all=-N -l")
	}
	if r.withRaceConditionDetection {
		args = append(args, "-race")
	}
	args = append(args, "-o", r.targetPath)

	proc, err := r.Env().NewProcess("go", args...)
	if err != nil {
		return w.Wrapf(err, "cannot create go build process")
	}
	if r.out != nil {
		proc.WithOutput(r.out)
	}
	proc.WithDir(r.sourceDir)

	err = proc.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot run go build")
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

func (r *GoRunnerEnvironment) Runner(args ...string) (runners.Proc, error) {
	proc, err := r.Env().NewProcess(r.targetPath, args...)
	if err != nil {
		return nil, err
	}
	proc.WithDir(r.sourceDir)
	return proc, nil
}

func (r *GoRunnerEnvironment) UsedCache() bool {
	return r.usedCache
}

func (r *GoRunnerEnvironment) WithRaceConditionDetection(b bool) {
	r.withRaceConditionDetection = b

}

func (r *GoRunnerEnvironment) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	r.Env().WithEnvironmentVariables(ctx, envs...)
}

func (r *GoRunnerEnvironment) WithFile(file string, location string) {
	if r.docker != nil {
		r.docker.WithMount(file, location)
	}
}

func (r *GoRunnerEnvironment) WithPort(ctx context.Context, port uint32) {
	if r.docker != nil {
		r.docker.WithPort(ctx, uint16(port))
	}
}
