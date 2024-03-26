package runners

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"

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

	// for logging
	w *wool.Wool

	// for output
	outLock sync.Mutex
	out     io.Writer

	// context
	ctx    context.Context
	cancel func()

	// wait for the logs to be written
	wg  sync.WaitGroup
	pid int
}

func NewProcess(ctx context.Context, bin string, args ...string) (*Process, error) {
	w := wool.Get(ctx).In("runner")
	if _, err := exec.LookPath(bin); err != nil {
		return nil, w.Wrapf(err, "cannot find <%s>", bin)
	}
	ctx, cancel := context.WithCancel(ctx)
	runner := &Process{
		bin:      bin,
		args:     args,
		finished: false,
		w:        w,
		out:      w,
		ctx:      ctx,
		cancel:   cancel,
	}
	return runner, nil
}

func (runner *Process) WithDir(dir string) *Process {
	runner.dir = dir
	return runner
}

func (runner *Process) WithEnvironmentVariables(envs ...string) *Process {
	runner.envs = append(runner.envs, envs...)
	return runner
}

func (runner *Process) WithDebug(debug bool) *Process {
	runner.debug = debug
	return runner
}

func (runner *Process) WithOut(out io.Writer) {
	runner.out = out
}

func (runner *Process) Init(_ context.Context) error {
	return nil
}

// Run execute and wait: great for tasks that we expect to finish
func (runner *Process) Run(ctx context.Context) error {
	// #nosec G204
	runner.cmd = exec.CommandContext(ctx, runner.bin, runner.args...)
	stdout, err := runner.cmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "cannot create stdout pipe")
	}
	defer stdout.Close()

	stderr, err := runner.cmd.StderrPipe()
	if err != nil {
		return errors.Wrap(err, "cannot create stderr pipe")
	}
	defer stderr.Close()

	reader := NewMultiReader(stdout, stderr)

	// When we are done, we want to close the ForwardLogs
	runner.ForwardLogs(reader)

	if runner.dir != "" {
		runner.cmd.Dir = runner.dir
	}
	runner.cmd.Env = runner.envs

	err = runner.cmd.Run()

	if err != nil {
		return runner.w.Wrapf(err, "cannot run command: %s %s", runner.cmd.Path, runner.cmd.Args)
	}
	runner.cancel()

	runner.wg.Wait()

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

func (runner *Process) ForwardLogs(reader io.Reader) {
	runner.wg.Add(1)
	scanner := bufio.NewScanner(reader)
	output := make(chan []byte)
	go func() {
		defer runner.wg.Done()
		for {
			select {
			case <-runner.ctx.Done():
				return
			default:
				for scanner.Scan() {
					output <- []byte(strings.TrimSpace(scanner.Text()))
				}
				if err := scanner.Err(); err != nil {
					output <- []byte(strings.TrimSpace(err.Error()))
				}

			}
		}
	}()
	go func() {
		for out := range output {
			runner.outLock.Lock()
			_, _ = runner.out.Write(out)
			runner.outLock.Unlock()
		}
	}()
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

type MultiReader struct {
	readers []io.Reader
	index   int
}

func NewMultiReader(readers ...io.Reader) *MultiReader {
	return &MultiReader{
		readers: readers,
		index:   0,
	}
}

func (mrc *MultiReader) Read(p []byte) (n int, err error) {
	for mrc.index < len(mrc.readers) {
		n, err = mrc.readers[mrc.index].Read(p)
		if err != io.EOF {
			return n, err
		}
		mrc.index++
	}
	return 0, io.EOF
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
	reader := NewMultiReader(stdout, stderr)

	runner.ForwardLogs(reader)

	if runner.dir != "" {
		runner.cmd.Dir = runner.dir
	}

	runner.cmd.Env = runner.envs

	err = runner.cmd.Start()
	if err != nil {
		return runner.w.Wrapf(err, "cannot run command")
	}
	runner.pid = runner.cmd.Process.Pid
	runner.w.Debug("started process", wool.Field("pid", runner.pid))
	return nil
}
