package runners

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/pkg/errors"

	"github.com/codefly-dev/core/wool"
)

/*
Still not quite correct
*/

type Runner struct {
	name  string
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
	wg sync.WaitGroup
}

func NewRunner(ctx context.Context, name string, bin string, args ...string) (*Runner, error) {
	w := wool.Get(ctx).In("runner")
	ctx, cancel := context.WithCancel(ctx)
	runner := &Runner{
		name:     name,
		bin:      bin,
		args:     args,
		finished: false,
		w:        w,
		out:      w,
		ctx:      ctx,
		cancel:   cancel,
	}
	// #nosec G204
	runner.cmd = exec.CommandContext(ctx, bin, args...)
	return runner, nil
}

func (runner *Runner) WithDir(dir string) *Runner {
	runner.dir = dir
	runner.cmd.Dir = dir
	return runner
}

func (runner *Runner) WithEnvs(envs []string) *Runner {
	runner.envs = envs
	runner.cmd.Env = envs
	return runner
}

func (runner *Runner) WithDebug(debug bool) *Runner {
	runner.debug = debug
	return runner
}

func (runner *Runner) WithOut(out io.Writer) {
	runner.out = out
}

// Run execute and wait: great for tasks that we expect to finish
func (runner *Runner) Run() error {
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

	runner.ForwardLogs(reader)

	// When we are done, we want to close the ForwardLogs
	err = runner.cmd.Run()

	if err != nil {
		return runner.w.Wrapf(err, "cannot run command")
	}
	runner.cancel()

	runner.wg.Wait()

	return nil
}

func (runner *Runner) Wait() error {
	return runner.cmd.Wait()
}

func (runner *Runner) Finished() bool {
	return false
}

func (runner *Runner) ForwardLogs(reader io.Reader) {
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

				//if err := scanner.Err(); err != nil {
				//	output <- []byte(strings.TrimSpace(err.Error()))
				//}

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

func (runner *Runner) Finish() {
	runner.finished = true
}

func (runner *Runner) Stop() error {
	runner.cancel()
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

// Start execute in a go-routine
func (runner *Runner) Start() error {
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

	// When we are done, we want to close the ForwardLogs
	err = runner.cmd.Start()
	if err != nil {
		return runner.w.Wrapf(err, "cannot run command")
	}
	return nil
}
