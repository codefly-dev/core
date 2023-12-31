package runners

import (
	"context"
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
		return nil, w.Wrapf(err, "cannot create wrapped command")
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
	cmd := exec.CommandContext(ctx, r.Bin, r.Args...)
	cmd.Dir = r.Dir
	cmd.Env = r.Envs

	run, err := NewWrappedCmd(cmd, w)
	if err != nil {
		return w.Wrapf(err, "cannot create wrapped command")
	}
	err = run.Run()
	if err != nil {
		return w.Wrapf(err, "cannot start command")
	}
	return nil
}
