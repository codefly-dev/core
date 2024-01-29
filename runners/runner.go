package runners

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/codefly-dev/core/wool"
)

type Runner struct {
	Name  string
	Bin   string
	Args  []string
	Dir   string
	Debug bool
	Envs  []string

	// internal
	cmd *exec.Cmd
}

func (r *Runner) Start(ctx context.Context) (*WrappedCmdOutput, error) {
	w := wool.Get(ctx).In("runner")
	w.Trace("in runner", wool.Field("bin", r.Bin), wool.Field("args", r.Args))
	// #nosec G204
	cmd := exec.CommandContext(ctx, r.Bin, r.Args...)
	cmd.Dir = r.Dir
	cmd.Env = r.Envs

	run, err := NewWrappedCmd(cmd, w)
	if err != nil {
		return nil, w.Wrapf(err, "cannot createAndWait wrapped command")
	}
	out, err := run.Start(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot start command")
	}
	return out, nil
}

func (r *Runner) Run(ctx context.Context) error {
	w := wool.Get(ctx).In("runner")
	w.Debug("in runner")
	// #nosec G204
	r.cmd = exec.CommandContext(ctx, r.Bin, r.Args...)
	r.cmd.Dir = r.Dir
	r.cmd.Env = r.Envs

	run, err := NewWrappedCmd(r.cmd, w)
	if err != nil {
		return w.Wrapf(err, "cannot createAndWait wrapped command")
	}
	err = run.Run()
	if err != nil {
		return w.Wrapf(err, "cannot start command")
	}
	return nil
}

func (r *Runner) Kill(_ context.Context) error {
	if r == nil {
		return nil
	}
	if r.cmd == nil {
		return nil
	}
	if r.cmd.Process == nil {
		return nil
	}
	err := r.cmd.Process.Kill()
	if err != nil {
		return fmt.Errorf("cannot kill process: %w", err)
	}
	return nil
}
