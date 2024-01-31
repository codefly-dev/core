package golang

import (
	"context"
	"fmt"
	"io"
	"path"

	"github.com/codefly-dev/core/runners"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/wool"
)

type Runner struct {
	name string
	dir  string
	args []string
	envs []string

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
	worker    *runners.Runner
}

func NewRunner(ctx context.Context, name string, dir string) (*Runner, error) {
	if ok, err := shared.CheckDirectory(ctx, dir); err != nil || !ok {
		return nil, fmt.Errorf("directory %s does not exist", dir)
	}
	// Default dependencies
	requirements := builders.NewDependencies("go", builders.NewDependency(dir).WithPathSelect(shared.NewSelect("*.go")))
	return &Runner{
		name:         name,
		dir:          dir,
		requirements: requirements,
	}, nil
}

func (runner *Runner) WithEnvs(envs []string) *Runner {
	runner.envs = envs
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
	runner.cacheDir = path.Join(runner.dir, ".cache")
	_, err := shared.CheckDirectoryOrCreate(ctx, runner.cacheDir)
	if err != nil {
		return w.Wrapf(err, "cannot create cache directory")
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
	builder, err := runners.NewRunner(ctx, "build-debug-binary", "go", args...)
	if err != nil {
		return w.Wrapf(err, "can't create runner")
	}
	builder.WithDir(runner.dir).WithDebug(runner.debug).WithEnvs(runner.envs).WithOut(runner.out)
	err = builder.Run()
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
	builder, err := runners.NewRunner(ctx, "build-normal-binary", "go", args...)
	if err != nil {
		return w.Wrapf(err, "can't create runner")
	}
	builder.WithDir(runner.dir).WithDebug(runner.debug).WithEnvs(runner.envs).WithOut(runner.out)
	err = builder.Run()
	if err != nil {
		return w.Wrapf(err, "cannot build binary")
	}
	return nil
}

func (runner *Runner) Start(ctx context.Context) error {
	w := wool.Get(ctx).In("go/runner")
	worker, err := runners.NewRunner(ctx, runner.name, runner.target)
	if err != nil {
		return w.Wrapf(err, "can't create runner")
	}

	worker.WithDir(runner.dir).WithEnvs(runner.envs).WithOut(runner.out)
	runner.worker = worker
	err = runner.worker.Start()
	if err != nil {
		return w.Wrapf(err, "cannot start binary")
	}
	return nil
}

func (runner *Runner) CacheDir() string {
	return runner.cacheDir
}

func (runner *Runner) Stop() error {
	if runner == nil {
		return fmt.Errorf("stopping a non-running go")
	}
	return runner.worker.Stop()
}
