package shared

import (
	"bufio"
	"bytes"
	"github.com/pkg/errors"
	"io"
	"os/exec"
)

func WrapStart(cmd *exec.Cmd, logger BaseLogger) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "cannot create stdout pipe")
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return errors.Wrap(err, "cannot create stderr pipe")
	}

	go ForwardLogs(stdout, logger)

	//	catch the error
	var b bytes.Buffer
	w := bufio.NewWriter(&b)
	go ForwardLogs(stderr, w)

	err = cmd.Start()
	if err != nil {
		w.Flush()
		return errors.Wrapf(err, "cannot run command: %s", b.String())
	}
	return nil
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
