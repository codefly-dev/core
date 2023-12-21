package shared

import (
	"bufio"
	"bytes"
	"io"
	"os/exec"

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

type Event struct {
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
	Events chan Event
}

func (run *WrappedCmd) Start() (*WrappedCmdOutput, error) {
	stdout, err := run.cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Wrap(err, "cannot create stdout pipe")
	}

	stderr, err := run.cmd.StderrPipe()
	if err != nil {
		return nil, errors.Wrap(err, "cannot create stderr pipe")
	}

	events := make(chan Event, 1)
	out := &WrappedCmdOutput{Events: events}
	go ForwardLogs(stdout, run.writer)

	//	catch the error
	var b bytes.Buffer
	w := bufio.NewWriter(&b)
	go ForwardLogs(stderr, w)

	err = run.cmd.Start()
	if err != nil {
		return nil, errors.Wrap(err, "cannot start command")
	}
	out.PID = run.cmd.Process.Pid
	go func() {
		err := run.cmd.Wait()
		if err != nil {
			out.Events <- Event{Err: err, Message: b.String()}
			return
		}
	}()
	return out, nil
}

func ForwardLogs(r io.ReadCloser, w io.Writer) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		_, _ = w.Write([]byte(scanner.Text()))
	}

	if err := scanner.Err(); err != nil {
		_, _ = w.Write([]byte(err.Error()))
	}

	_ = r.Close()
}
