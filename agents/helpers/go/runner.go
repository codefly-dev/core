package golang

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/shared"
)

type Runner struct {
	Name  string
	Dir   string
	Args  []string
	Envs  []string
	Debug bool

	ForwardLogger io.Writer

	Cmd   *exec.Cmd
	clean func()

	// internal
	killed bool
}

func (g *Runner) Init(ctx context.Context) error {
	w := wool.Get(ctx).In("GoRunner")
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

func (g *Runner) Run(ctx context.Context) (*services.TrackedProcess, error) {
	w := wool.Get(ctx).In("GoRunner")
	// Setup variables once
	g.Cmd.Env = g.Envs

	err := shared.WrapStart(g.Cmd, g.ForwardLogger)
	if err != nil {
		return nil, w.Wrapf(err, "cannot wrap execution of cmd")
	}
	if g.killed {
		return &services.TrackedProcess{PID: g.Cmd.Process.Pid, Killed: true}, nil
	}
	return &services.TrackedProcess{PID: g.Cmd.Process.Pid}, nil
}

func (g *Runner) debugCmd(ctx context.Context) (func(), error) {
	w := wool.Get(ctx).In("GoRunner")
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
	cmd = exec.CommandContext(ctx, target)
	cmd.Dir = g.Dir
	g.Cmd = cmd
	return clean, nil
}

func (g *Runner) NormalCmd(ctx context.Context) (func(), error) {
	w := wool.Get(ctx).In("GoRunner")
	w.Info("building binary in debug mode")
	// Build with debug options
	tmp := os.TempDir()
	target := fmt.Sprintf("%s/main", tmp)
	clean := func() {
		_ = os.Remove(target)
	}

	args := []string{"build", "-o", target}
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

	cmd = exec.CommandContext(ctx, target)
	cmd.Dir = g.Dir
	g.Cmd = cmd
	return clean, nil
}

func (g *Runner) Kill(ctx context.Context) error {
	w := wool.Get(ctx).In("GoRunner::Kill")
	if g.killed {
		return nil
	}
	g.killed = true
	if g.clean != nil {
		g.clean()
	}
	err := g.Cmd.Process.Kill()
	if err != nil {
		err = g.Cmd.Wait()
		if err != nil {
			return w.Wrapf(err, "cannot wait for process to die")
		}
		return w.Wrapf(err, "cannot kill process")
	}
	return nil
}
