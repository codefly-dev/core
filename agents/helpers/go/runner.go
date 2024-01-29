package golang

import (
	"context"
	"fmt"
	"os/exec"
	"path"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/runners"

	"github.com/codefly-dev/core/wool"
)

type Runner struct {
	Name string
	Dir  string
	Args []string
	Envs []string

	// Build with debug symbols
	Debug bool
	// Build with race condition detection
	RaceConditionDetection bool

	// Used to cache the binary
	Requirements *builders.Dependencies

	// internal
	cacheDir string
	killed   bool
	target   string

	cmd *exec.Cmd
}

func (g *Runner) Init(ctx context.Context) error {
	w := wool.Get(ctx).In("go/runner")
	// Setup cache for binaries
	g.cacheDir = path.Join(g.Dir, ".cache")
	_, err := shared.CheckDirectoryOrCreate(ctx, g.cacheDir)
	if err != nil {
		return w.Wrapf(err, "cannot create cache directory")
	}
	if !g.Debug {
		err = g.NormalCmd(ctx)
	} else {
		err = g.debugCmd(ctx)
	}
	if err != nil {
		return w.Wrapf(err, "cannot build binary")
	}
	return nil
}

func (g *Runner) Start(ctx context.Context) (*runners.WrappedCmdOutput, error) {
	w := wool.Get(ctx).In("go/runner")
	w.Debug("in runner")
	// #nosec G204
	cmd := exec.CommandContext(ctx, g.target)
	g.cmd = cmd
	cmd.Dir = g.Dir
	// Setup variables once
	cmd.Env = g.Envs

	run, err := runners.NewWrappedCmd(cmd, w)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create wrapped command")
	}
	out, err := run.Start(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot start command")
	}

	if g.killed {
		return out, nil
	}
	return out, nil
}

func (g *Runner) debugCmd(ctx context.Context) error {
	w := wool.Get(ctx).In("go/runner")
	hash, err := g.Requirements.Hash(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot get hash")
	}
	g.target = path.Join(g.cacheDir, fmt.Sprintf("%s-%s", hash, "debug"))
	if shared.FileExists(g.target) {
		w.Debug("found a cache binary: don't work until we have to!")
		return nil
	}
	w.Info("building binary in debug mode")
	// clean cache
	err = shared.EmptyDir(g.cacheDir)
	if err != nil {
		return w.Wrapf(err, "cannot clean cache")
	}

	args := []string{"build", "-gcflags", "all=-N -l"}
	if g.RaceConditionDetection {
		args = append(args, "-race")
	}
	args = append(args, "-o", g.target)
	args = append(args, g.Args...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = g.Dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		g.killed = true
		output := fmt.Errorf(string(out))
		return output
	}
	return nil
}

func (g *Runner) NormalCmd(ctx context.Context) error {
	w := wool.Get(ctx).In("go/runner")
	hash, err := g.Requirements.Hash(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot get hash")
	}
	g.target = path.Join(g.cacheDir, fmt.Sprintf("%s-%s", hash, "debug"))
	if shared.FileExists(g.target) {
		w.Debug("found a cache binary: don't work until we have to!")
		return nil
	}
	w.Info("building go binary")
	// clean cache
	err = shared.EmptyDir(g.cacheDir)
	if err != nil {
		return w.Wrapf(err, "cannot clean cache")
	}

	args := []string{"build"}
	if g.RaceConditionDetection {
		args = append(args, "-race")
	}
	args = append(args, "-o", g.target)

	args = append(args, g.Args...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = g.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		g.killed = true
		output := fmt.Errorf(string(out))
		return output
	}
	w.Info("built", wool.StatusOK())
	return nil
}

func (g *Runner) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("go/runner::Kill")
	if g == nil {
		return nil
	}
	if g.killed {
		return nil
	}
	g.killed = true
	if g.cmd == nil {
		return nil
	}
	// Check if the process is already dead
	if g.cmd.ProcessState != nil {
		return nil
	}
	err := g.cmd.Process.Kill()
	if err != nil {
		return w.Wrapf(err, "cannot kill process")
	}
	return nil
}
