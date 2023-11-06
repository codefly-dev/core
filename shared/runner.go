package shared

import (
	"bufio"
	"bytes"
	"io"
	"os/exec"

	"github.com/pkg/errors"
)

type Runner struct {
	bin    string
	args   []string
	dir    Dir
	out    BaseLogger
	logger BaseLogger // debugging override
}

func NewRunner(bin string, dir Dir, args []string, out BaseLogger, override BaseLogger) *Runner {
	logger := NewLogger("shared.NewRunner").IfNot(override)
	logger.Debugf("creating runner for %s", bin)
	return &Runner{logger: logger, out: out, dir: dir, bin: bin, args: args}
}

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

	// catch the error
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

func (r *Runner) Run() error {
	cmd := exec.Command(r.bin, r.args...)
	cmd.Dir = r.dir.Relative()
	return WrapStart(cmd, r.out)
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
