package base

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"strings"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/wool"
)

type LocalEnvironment struct {
	dir string

	envs []string

	out io.Writer

	ctx context.Context
}

func (local *LocalEnvironment) Clear(context.Context) error {
	return nil
}

// NewLocalEnvironment creates a new docker runner
func NewLocalEnvironment(ctx context.Context, dir string) (*LocalEnvironment, error) {
	w := wool.Get(ctx).In("NewDockerRunner")
	return &LocalEnvironment{
		out: w,
		dir: dir,
	}, nil
}

func (local *LocalEnvironment) Init(ctx context.Context) error {
	local.ctx = ctx
	return nil
}

func (local *LocalEnvironment) WithEnvironmentVariables(envs ...configurations.EnvironmentVariable) {
	local.envs = append(local.envs, configurations.EnvironmentVariableAsStrings(envs)...)
}

func (local *LocalEnvironment) Shutdown(context.Context) error {

	return nil
}

type LocalProc struct {
	env     *LocalEnvironment
	output  io.Writer
	cmd     []string
	exec    *exec.Cmd
	stopped chan interface{}
	envs    []string
}

func (proc *LocalProc) WithEnvironmentVariables(envs ...configurations.EnvironmentVariable) {
	proc.envs = append(proc.envs, configurations.EnvironmentVariableAsStrings(envs)...)
}

func (local *LocalEnvironment) NewProcess(bin string, args ...string) (Proc, error) {
	if _, err := exec.LookPath(bin); err != nil {
		return nil, err
	}
	cmd := append([]string{bin}, args...)
	return &LocalProc{
		env:     local,
		cmd:     cmd,
		output:  local.out,
		stopped: make(chan interface{})}, nil
}

func (local *LocalEnvironment) Stop(context.Context) error {
	return nil
}

func (proc *LocalProc) Run(ctx context.Context) error {
	w := wool.Get(ctx).In("LocalProc.Run")
	w.Debug("running process", wool.Field("cmd", proc.cmd), wool.Field("envs", proc.env.envs))
	err := proc.start(ctx)
	if err != nil {
		return err
	}
	w.Debug("waiting for process to finish or be killed")
	// Create a channel to receive the result of proc.exec.Wait()
	done := make(chan error, 1)
	go func() {
		done <- proc.exec.Wait()
	}()

	// Use a select statement to wait for either the process to finish or the context to be cancelled
	select {
	case err := <-done:
		if err != nil {
			w.Focus(err.Error())
			return w.Wrapf(err, "cannot wait for process")
		}
	case <-proc.stopped:
		w.Debug("process was killed")
	}

	w.Debug("done")
	return nil
}

func (proc *LocalProc) Start(ctx context.Context) error {
	w := wool.Get(ctx).In("LocalProc.Start")
	w.Debug("starting process", wool.Field("cmd", proc.cmd), wool.Field("envs", proc.env.envs))
	return proc.start(ctx)

}
func (proc *LocalProc) start(ctx context.Context) error {
	w := wool.Get(ctx).In("LocalProc.start")
	// #nosec G204
	cmd := exec.CommandContext(ctx, proc.cmd[0], proc.cmd[1:]...)
	cmd.Dir = proc.env.dir
	cmd.Env = proc.env.envs
	cmd.Env = append(cmd.Env, proc.envs...)

	// start and get the logs
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	err = cmd.Start()
	if err != nil {
		return err
	}
	proc.exec = cmd

	go func() {
		defer stdout.Close()
		proc.Forward(ctx, stdout)
	}()
	go func() {
		defer stderr.Close()
		proc.Forward(ctx, stderr)
	}()
	w.Debug("done")
	return nil
}

func (proc *LocalProc) Forward(_ context.Context, w io.Reader) {
	// Create a new scanner and set the split function to bufio.ScanLines
	scanner := bufio.NewScanner(w)
	scanner.Split(bufio.ScanLines)

	// Scan the standard output line by line
	for scanner.Scan() {
		line := scanner.Text()
		// Write each line to the output
		_, err := proc.output.Write([]byte(strings.TrimSpace(line)))
		if err != nil {
			_, _ = proc.output.Write([]byte(err.Error()))
			return
		}
	}

	if scanner.Err() != nil {
		return
	}
}

func (proc *LocalProc) WithOutput(output io.Writer) {
	proc.output = output
}

func (proc *LocalProc) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("LocalProc.Stop")
	w.Debug("stopping process")
	go func() {
		proc.stopped <- struct{}{}
	}()
	err := proc.exec.Process.Kill()
	if err != nil {
		return w.Wrapf(err, "cannot kill process")
	}
	return nil
}
