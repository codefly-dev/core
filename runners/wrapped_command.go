package runners

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"

	"strings"

	"github.com/codefly-dev/core/wool"
	"github.com/pkg/errors"
)

func RequireExec(bins ...string) ([]string, bool) {
	var missing []string
	for _, bin := range bins {
		_, err := exec.LookPath(bin)
		if err != nil {
			missing = append(missing, bin)
		}
	}
	return missing, len(missing) == 0
}

type RunnerEvent struct {
	Err     error
	Message string
}

type WrappedCmd struct {
	cmd    *exec.Cmd
	writer io.Writer
}

func NewWrappedCmd(cmd *exec.Cmd, writer io.Writer) (*WrappedCmd, error) {
	w := &WrappedCmd{
		cmd:    cmd,
		writer: writer,
	}
	return w, nil
}

type WrappedCmdOutput struct {
	PID    int
	Events chan RunnerEvent
}

func (run *WrappedCmd) Start(ctx context.Context) (*WrappedCmdOutput, error) {
	w := wool.Get(ctx).In("WrappedCommand")
	w.Debug("starting command", wool.Field("cmd", run.cmd.Args))
	stdout, err := run.cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Wrap(err, "cannot createAndWait stdout pipe")
	}

	stderr, err := run.cmd.StderrPipe()
	if err != nil {
		return nil, errors.Wrap(err, "cannot createAndWait stderr pipe")
	}

	events := make(chan RunnerEvent, 1)
	out := &WrappedCmdOutput{Events: events}

	var b bytes.Buffer
	writer := bufio.NewWriter(&b)

	go ForwardLogs(stdout, run.writer)
	go ForwardLogs(stderr, run.writer, writer)

	err = run.cmd.Start()
	if err != nil {
		return nil, errors.Wrap(err, "cannot start command")
	}
	out.PID = run.cmd.Process.Pid
	go func() {
		err := run.cmd.Wait()
		if err != nil {
			out.Events <- RunnerEvent{Err: err, Message: b.String()}
			return
		}
	}()
	return out, nil
}

func (run *WrappedCmd) Run() error {
	stdout, err := run.cmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "cannot createAndWait stdout pipe")
	}

	stderr, err := run.cmd.StderrPipe()
	if err != nil {
		return errors.Wrap(err, "cannot createAndWait stderr pipe")
	}

	go ForwardLogs(stdout, run.writer)
	go ForwardLogs(stderr, run.writer)

	return run.cmd.Run()
}

func ForwardLogs(r io.ReadCloser, ws ...io.Writer) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		for _, w := range ws {
			_, _ = w.Write([]byte(strings.TrimSpace(scanner.Text())))
		}
	}

	if err := scanner.Err(); err != nil {
		for _, w := range ws {
			_, _ = w.Write([]byte(strings.TrimSpace(err.Error())))
		}
	}

	_ = r.Close()
}
