package golang

import (
	"context"
	"fmt"
	"io"
	"path"

	"github.com/codefly-dev/core/configurations"

	"github.com/codefly-dev/core/runners"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/wool"
)

type Runner struct {
	dir  string
	args []string
	envs []configurations.EnvironmentVariable

	// Build with debug symbols
	debug bool
	// Build with race condition detection
	raceConditionDetection bool

	// Used to cache the binary
	requirements *builders.Dependencies

	out io.Writer

	// internal
	cacheDir  string
	target    string
	usedCache bool
	worker    runners.Runner

	withModules bool
}

func (runner *Runner) Run(_ context.Context) error {
	//TODO implement me
	panic("implement me")
}

func NewRunner(ctx context.Context, dir string) (*Runner, error) {
	if ok, err := shared.CheckDirectory(ctx, dir); err != nil || !ok {
		return nil, fmt.Errorf("directory %s does not exist", dir)
	}
	// Default dependencies
	requirements := builders.NewDependencies("go", builders.NewDependency(dir).WithPathSelect(shared.NewSelect("*.go")))
	return &Runner{
		dir:          dir,
		cacheDir:     path.Join(dir, ".cache/binaries"),
		requirements: requirements,
	}, nil
}

func (runner *Runner) WithEnvs(envs []configurations.EnvironmentVariable) *Runner {
	runner.envs = append(runner.envs, envs...)
	return runner
}

func (runner *Runner) WithDebug(debug bool) *Runner {
	runner.debug = debug
	return runner
}

func (runner *Runner) WithOut(out io.Writer) *Runner {
	runner.out = out
	return runner
}

func (runner *Runner) WithRaceConditionDetection(raceConditionDection bool) *Runner {
	runner.raceConditionDetection = raceConditionDection
	return runner
}

func (runner *Runner) WithRequirements(requirements *builders.Dependencies) *Runner {
	runner.requirements = requirements
	return runner
}

func (runner *Runner) Init(ctx context.Context) error {
	w := wool.Get(ctx).In("go/runner")
	// Setup cache for binaries
	_, err := shared.CheckDirectoryOrCreate(ctx, runner.cacheDir)
	if err != nil {
		return w.Wrapf(err, "cannot create cache directory")
	}
	// Run go mod tidy
	helper := Go{Dir: runner.dir}
	runner.withModules, err = helper.ModTidy(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot run go mod tidy")
	}
	if !runner.withModules {
		w.Warn("running without go modules: we make not guarantee!")
	}
	// Run go mod download
	_, err = helper.ModDowload(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot run go mod download")
	}

	if runner.debug {
		err = runner.debugCmd(ctx)
	} else {
		err = runner.NormalCmd(ctx)
	}
	if err != nil {
		return w.Wrapf(err, "cannot build binary")
	}
	return nil
}

func (runner *Runner) UsedCache() bool {
	return runner.usedCache
}

func (runner *Runner) debugCmd(ctx context.Context) error {
	w := wool.Get(ctx).In("go/builder")
	runner.usedCache = false
	hash, err := runner.requirements.Hash(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot get hash")
	}
	runner.target = path.Join(runner.cacheDir, fmt.Sprintf("%s-%s", hash, "debug"))
	w.Debug("build target", wool.Field("target", runner.target))
	if shared.FileExists(runner.target) {
		w.Debug("found a cache binary: don't work until we have to!")
		runner.usedCache = true
		return nil
	}
	w.Info("building binary in debug mode")
	// clean cache
	err = shared.EmptyDir(runner.cacheDir)
	if err != nil {
		return w.Wrapf(err, "cannot clean cache")
	}

	args := []string{"build", "-gcflags", "all=-N -l"}
	if runner.raceConditionDetection {
		args = append(args, "-race")
	}
	args = append(args, "-o", runner.target)
	args = append(args, runner.args...)
	// Call a builder!
	builder, err := runners.NewProcess(ctx, "go", args...)
	if err != nil {
		return w.Wrapf(err, "can't create runner")
	}
	builder.WithDir(runner.dir)
	builder.WithDebug(runner.debug)
	builder.WithEnvironmentVariables(runner.envs...)
	if !runner.withModules {
		builder.WithEnvironmentVariables(
			configurations.Env("GO111MODULE", "off"),
			configurations.Env("GOCACHE", runner.cacheDir))
	}
	err = builder.WithOut(runner.out)
	if err != nil {
		return w.Wrapf(err, "cannot set output")
	}
	err = builder.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot build binary")
	}
	return nil
}

func (runner *Runner) NormalCmd(ctx context.Context) error {
	w := wool.Get(ctx).In("go/builder")
	runner.usedCache = false
	hash, err := runner.requirements.Hash(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot get hash")
	}
	runner.target = path.Join(runner.cacheDir, fmt.Sprintf("%s-%s", hash, "normal"))
	w.Debug("build target", wool.Field("target", runner.target))
	if shared.FileExists(runner.target) {
		w.Debug("found a cache binary: don't work until we have to!")
		runner.usedCache = true
		return nil
	}
	w.Info("building binary in debug mode")
	// clean cache
	err = shared.EmptyDir(runner.cacheDir)
	if err != nil {
		return w.Wrapf(err, "cannot clean cache")
	}

	args := []string{"build"}
	if runner.raceConditionDetection {
		args = append(args, "-race")
	}
	args = append(args, "-o", runner.target)
	args = append(args, runner.args...)
	// Call a builder!
	builder, err := runners.NewProcess(ctx, "go", args...)
	if err != nil {
		return w.Wrapf(err, "can't create runner")
	}
	builder.WithDir(runner.dir)
	builder.WithDebug(runner.debug)
	builder.WithEnvironmentVariables(runner.envs...)
	if !runner.withModules {
		builder.WithEnvironmentVariables(
			configurations.Env("GO111MODULE", "off"),
			configurations.Env("GOCACHE", runner.cacheDir))
	}
	err = builder.WithOut(runner.out)
	if err != nil {
		return w.Wrapf(err, "cannot set output")
	}
	err = builder.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot build binary")
	}
	return nil
}

func (runner *Runner) Start(ctx context.Context) error {
	w := wool.Get(ctx).In("go/runner")
	worker, err := runners.NewProcess(ctx, runner.target, runner.args...)
	if err != nil {
		return w.Wrapf(err, "can't create runner")
	}

	worker.WithDir(runner.dir)
	worker.WithEnvironmentVariables(runner.envs...)
	err = worker.WithOut(runner.out)
	if err != nil {
		return w.Wrapf(err, "cannot set output")
	}
	runner.worker = worker
	err = runner.worker.Start(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot start binary")
	}
	return nil
}

func (runner *Runner) CacheDir() string {
	return runner.cacheDir
}

func (runner *Runner) Stop() error {
	if runner == nil || runner.worker == nil {
		return nil
	}
	return runner.worker.Stop()
}

func (runner *Runner) WithCache(location string) {
	runner.cacheDir = path.Join(location, "binaries")
}

func (runner *Runner) WithArgs(args []string) {
	runner.args = append(runner.args, args...)
}
