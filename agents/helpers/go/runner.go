package golang

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/shared"
)

type Runner struct {
	Name  string
	Dir   string
	Args  []string
	Envs  []string
	Debug bool

	clean func()

	// internal
	killed bool
	target string
	cmd    *exec.Cmd
}

func (g *Runner) Init(ctx context.Context) error {
	w := wool.Get(ctx).In("go/runner")
	g.killed = false
	var clean func()
	var err error
	if !g.Debug {
		clean, err = g.NormalCmd(ctx)
	} else {
		clean, err = g.debugCmd(ctx)
	}
	if err != nil {
		return w.Wrapf(err, "cannot build binary")
	}
	g.clean = clean
	return nil
}

func (g *Runner) Run(ctx context.Context) (*shared.WrappedCmdOutput, error) {
	w := wool.Get(ctx).In("go/runner")
	w.Debug("in runner")
	// #nosec G204
	cmd := exec.CommandContext(ctx, g.target)
	g.cmd = cmd
	cmd.Dir = g.Dir
	// Setup variables once
	cmd.Env = g.Envs

	run, err := shared.NewWrappedCmd(cmd, w)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create wrapped command")
	}
	out, err := run.Start()
	if err != nil {
		return nil, w.Wrapf(err, "cannot start command")
	}

	if g.killed {
		return out, nil
	}
	return out, nil
}

func (g *Runner) debugCmd(ctx context.Context) (func(), error) {
	w := wool.Get(ctx).In("go/runner")
	w.Info("building binary in debug mode")
	// Build with debug options
	tmp := os.TempDir()
	target := fmt.Sprintf("%s/main", tmp)
	clean := func() {
		_ = os.Remove(target)
	}

	args := []string{"build", "-gcflags", "all=-N -l", "-o", target}
	args = append(args, g.Args...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = g.Dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		g.killed = true
		output := fmt.Errorf(string(out))
		return nil, output
	}

	return clean, nil
}

func (g *Runner) NormalCmd(ctx context.Context) (func(), error) {
	w := wool.Get(ctx).In("go/runner")
	w.Info("building binary in debug mode")
	// Build with debug options
	tmp := os.TempDir()
	g.target = fmt.Sprintf("%s/main", tmp)
	clean := func() {
		_ = os.Remove(g.target)
	}

	args := []string{"build", "-o", g.target}
	args = append(args, g.Args...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = g.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		g.killed = true
		output := fmt.Errorf(string(out))
		return nil, output
	}
	w.Info("built", wool.StatusOK())

	return clean, nil
}

func (g *Runner) Kill(ctx context.Context) error {
	w := wool.Get(ctx).In("go/runner::Kill")
	if g.killed {
		return nil
	}
	g.killed = true
	if g.clean != nil {
		g.clean()
	}
	if g.cmd == nil {
		return nil
	}
	err := g.cmd.Process.Kill()
	if err != nil {
		err = g.cmd.Wait()
		if err != nil {
			return w.Wrapf(err, "cannot wait for process to die")
		}
		return w.Wrapf(err, "cannot kill process")
	}
	return nil
}
