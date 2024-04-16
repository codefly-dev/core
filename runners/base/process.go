package base

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/wool"
)

/*
Still not quite correct
*/

type Process struct {
	bin   string
	args  []string
	dir   string
	debug bool
	envs  []string

	// internal
	cmd      *exec.Cmd
	finished bool

	w *wool.Wool
	// for output
	out io.Writer

	// context
	ctx    context.Context
	cancel func()

	// wait for the logs to be written
	wg  sync.WaitGroup
	pid int
}

func NewProcess(ctx context.Context, bin string) (*Process, error) {
	w := wool.Get(ctx).In("runner")
	if _, err := exec.LookPath(bin); err != nil {
		return nil, w.Wrapf(err, "cannot find <%s>", bin)
	}
	ctx, cancel := context.WithCancel(ctx)
	runner := &Process{
		bin:      bin,
		finished: false,
		w:        w,
		out:      w,
		ctx:      ctx,
		cancel:   cancel,
	}
	return runner, nil
}

func (runner *Process) WithDir(dir string) {
	runner.dir = dir
}

func (runner *Process) WithEnvironmentVariables(envs ...configurations.EnvironmentVariable) {
	runner.envs = append(runner.envs, configurations.EnvironmentVariableAsStrings(envs)...)
}

func (runner *Process) WithDebug(debug bool) {
	runner.debug = debug
}

func (runner *Process) WithOutput(w io.Writer) {
	runner.out = w
}

func (runner *Process) Init(_ context.Context) error {
	return nil
}

// Run executes and wait
func (runner *Process) Run(ctx context.Context) error {
	if runner.out == nil {
		return errors.New("cannot run without output")
	}
	// #nosec G204
	runner.cmd = exec.CommandContext(ctx, runner.bin, runner.args...)

	stdout, err := runner.cmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "cannot create stdout pipe")
	}

	stderr, err := runner.cmd.StderrPipe()
	if err != nil {
		return errors.Wrap(err, "cannot create stderr pipe")
	}
	reader := io.MultiReader(stdout, stderr)

	ForwardLogs(ctx, &runner.wg, reader, runner.out)

	if runner.dir != "" {
		runner.cmd.Dir = runner.dir
	}

	runner.cmd.Env = os.Environ()
	runner.cmd.Env = append(runner.cmd.Env, runner.envs...)

	err = runner.cmd.Run()
	if err != nil {
		return runner.w.Wrapf(err, "cannot run command")
	}
	return nil
}

// Start executing and return
func (runner *Process) Start(ctx context.Context) error {
	// #nosec G204
	runner.cmd = exec.CommandContext(ctx, runner.bin, runner.args...)

	stdout, err := runner.cmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "cannot create stdout pipe")
	}

	stderr, err := runner.cmd.StderrPipe()
	if err != nil {
		return errors.Wrap(err, "cannot create stderr pipe")
	}
	reader := io.MultiReader(stdout, stderr)

	ForwardLogs(ctx, &runner.wg, reader, runner.out)

	if runner.dir != "" {
		runner.cmd.Dir = runner.dir
	}

	runner.cmd.Env = os.Environ()
	runner.cmd.Env = append(runner.cmd.Env, runner.envs...)

	err = runner.cmd.Start()
	if err != nil {
		return runner.w.Wrapf(err, "cannot run command")
	}
	runner.pid = runner.cmd.Process.Pid
	runner.w.Debug("started process", wool.Field("pid", runner.pid))
	return nil
}

func (runner *Process) Wait() error {
	if runner.finished {
		return nil
	}
	return runner.cmd.Wait()
}

func (runner *Process) Finished() bool {
	return false
}

func (runner *Process) Finish() {
	runner.finished = true
}

func (runner *Process) Stop() error {
	if runner == nil {
		return nil
	}
	if runner.cmd == nil {
		return nil
	}
	err := runner.cmd.Process.Signal(syscall.SIGTERM)
	if err != nil {
		return runner.w.Wrapf(err, "cannot sigterm process")
	}
	// Wait  bit
	<-time.After(1 * time.Second)
	runner.cancel()

	// Kill the process to be sure

	// Check if the process is still running
	err = runner.cmd.Process.Signal(syscall.Signal(0))
	if err == nil {
		// Process is still running, send SIGKILL
		err = runner.cmd.Process.Kill()
		if err != nil {
			return runner.w.Wrapf(err, "cannot kill process")
		}
	}

	return nil
}

func (runner *Process) WithArguments(args ...string) {
	runner.args = args
}

func (runner *Process) WithBin(bin string) {
	runner.bin = bin
}
