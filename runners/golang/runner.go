package golang

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"

	"github.com/codefly-dev/core/builders"

	"github.com/codefly-dev/core/configurations"

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
	local  *runners.LocalEnvironment

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
}

func (r *GoRunnerEnvironment) LocalCacheDir(ctx context.Context) string {
	var p string
	if r.docker != nil {
		p = path.Join(r.localCacheDir, "docker")
	} else {
		p = path.Join(r.localCacheDir, "local")
	}
	_, _ = shared.CheckDirectoryOrCreate(ctx, p)
	return p
}

func (r *GoRunnerEnvironment) WithDebugSymbol(debug bool) {
	r.withDebugSymbol = debug
}

func NewLocalGoRunner(ctx context.Context, dir string) (*GoRunnerEnvironment, error) {
	w := wool.Get(ctx).In("NewLocalGoRunner")
	w.Debug("creating local go runner", wool.Field("dir", dir))
	// Check that go in the path
	_, err := exec.LookPath("go")
	if err != nil {
		return nil, w.NewError("cannot find go in the path")
	}
	local, err := runners.NewLocalEnvironment(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create go local environment")
	}
	withGoModules := shared.FileExists(path.Join(dir, "go.mod"))
	if !withGoModules {
		w.Warn("running without go modules: not encouraged at all")
	} else {
		// Setup up the proper environment
		if v, ok := os.LookupEnv("GOMODCACHE"); ok {
			local.WithEnvironmentVariables(configurations.Env("GOMODCACHE", v))
		} else {
			if v, ok := os.LookupEnv("GOPATH"); ok {
				local.WithEnvironmentVariables(configurations.Env("GOPATH", v))
			}
		}

	}
	return &GoRunnerEnvironment{
		dir:           dir,
		local:         local,
		withGoModules: withGoModules,
		localCacheDir: path.Join(dir, "cache"),
	}, nil
}

func NewDockerGoRunner(ctx context.Context, image *configurations.DockerImage, dir string, name string) (*GoRunnerEnvironment, error) {
	w := wool.Get(ctx).In("NewDockerGoRunner")
	name = fmt.Sprintf("goland-%s", name)
	w.Debug("creating docker go runner", wool.Field("image", image), wool.Field("dir", dir), wool.Field("name", name))
	docker, err := runners.NewDockerEnvironment(ctx, image, dir, name)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create go docker environment")
	}
	docker.WithPause()
	withGoModules := shared.FileExists(path.Join(dir, "go.mod"))
	if !withGoModules {
		w.Warn("running without go modules: not encouraged at all")
	}

	return &GoRunnerEnvironment{
		dir:           dir,
		docker:        docker,
		withGoModules: withGoModules,
		localCacheDir: path.Join(dir, "cache"),
	}, nil
}

func (r *GoRunnerEnvironment) Env() runners.RunnerEnvironment {
	if r.docker != nil {
		return r.docker
	}
	return r.local
}

func (r *GoRunnerEnvironment) Setup(ctx context.Context) {
	if r.docker != nil {
		// Build
		r.docker.WithMount(r.LocalCacheDir(ctx), "/build")
		if r.goModCache != "" {
			r.docker.WithMount(r.goModCache, "/go/pkg/mod")
			return
		}
		// Setup up the proper environment
		if v, ok := os.LookupEnv("GOMODCACHE"); ok {
			// Mount
			r.docker.WithMount(v, "/go/pkg/mod")
		} else if v, ok := os.LookupEnv("GOPATH"); ok {
			// Mount
			r.docker.WithMount(path.Join(v, "pkg/mod"), "/go/pkg/mod")
		} else {
			// Use codefly configuration path
			goModCache := path.Join(configurations.WorkspaceConfigurationDir(), "go/pkg/mod")
			r.docker.WithMount(goModCache, "/go/pkg/mod")
		}
	}
	if r.local != nil {
		if r.goModCache != "" {
			r.Env().WithEnvironmentVariables(configurations.Env("GOMODCACHE", r.goModCache))
		}
		r.Env().WithEnvironmentVariables(configurations.Env("HOME", os.Getenv("HOME")))
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

	err = r.BuildBinary(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot build binary")
	}
	return nil
}

func (r *GoRunnerEnvironment) GoModuleHandling(ctx context.Context) error {
	w := wool.Get(ctx).In("goModuleHandling")
	proc, err := r.Env().NewProcess("go", "mod", "download")
	if err != nil {
		return w.Wrapf(err, "cannot create go mod download process")
	}
	if r.out != nil {
		proc.WithOutput(r.out)
	}
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
		builders.NewDependency(r.dir).WithPathSelect(shared.NewSelect("*.go")))

	w.Debug("start building")
	r.usedCache = false

	hash, err := r.requirements.Hash(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot get hash")
	}

	r.targetPath = r.BuildTargetPath(ctx, hash)

	cached := r.LocalTargetPath(ctx, hash)
	w.Debug("checking local cache", wool.FileField(cached))

	if shared.FileExists(cached) {
		w.Debug("found a cache binary: don't work until we have to!", wool.FileField(cached))
		r.usedCache = true
		return nil
	}
	// clean cache
	err = shared.EmptyDir(r.LocalCacheDir(ctx))
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

func (r *GoRunnerEnvironment) Clear(ctx context.Context) error {
	return r.Env().Clear(ctx)
}

func (r *GoRunnerEnvironment) WithLocalCacheDir(dir string) {
	r.localCacheDir = dir
}

func (r *GoRunnerEnvironment) NewProcess() (runners.Proc, error) {
	return r.Env().NewProcess(r.targetPath)
}

func (r *GoRunnerEnvironment) UsedCache() bool {
	return r.usedCache
}

func (r *GoRunnerEnvironment) WithRaceConditionDetection(b bool) {
	r.withRaceConditionDetection = b

}

func (r *GoRunnerEnvironment) WithEnvironmentVariables(envs ...configurations.EnvironmentVariable) {
	r.Env().WithEnvironmentVariables(envs...)
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
