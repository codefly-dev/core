package runners

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/agents/services"

	"github.com/pkg/errors"
)

type Runner struct {
	Name  string
	Bin   string
	Dir   string
	Args  []string
	Envs  []string
	Debug bool

	Wait bool

	Cmd *exec.Cmd

	// internal
	killed bool
}

func (g *Runner) Init(ctx context.Context) error {
	g.killed = false
	// #nosec G204
	g.Cmd = exec.CommandContext(ctx, g.Bin, g.Args...)
	return nil
}

func (g *Runner) Run(_ context.Context) (*services.TrackedProcess, error) {
	//// Setup variables once
	//g.Cmd.Env = g.Envs
	//g.Cmd.Dir = g.Dir
	//if g.Wait {
	//	err := WrapStartDebug(g.Cmd, g.AgentLogger)
	//	if err != nil {
	//		return nil, g.AgentLogger.Wrapf(err, "cannot wrap execution of cmd")
	//	}
	//} else {
	//	err := WrapStart(g.Cmd, g.ServiceLogger, g.AgentLogger)
	//	if err != nil {
	//		return nil, shared.Wrapf(err, "cannot wrap execution of cmd")
	//	}
	//}
	//if g.killed {
	//	return &services.TrackedProcess{PID: g.Cmd.Process.Pid, Killed: true}, nil
	//}
	//return &services.TrackedProcess{PID: g.Cmd.Process.Pid}, nil
	return nil, nil
}

func (g *Runner) Kill(ctx context.Context) error {
	w := wool.Get(ctx).In("GoRunner::Kill")
	if g.killed {
		return nil
	}
	g.killed = true
	err := g.Cmd.Process.Kill()
	if err != nil {
		err = g.Cmd.Wait()
		if err != nil {
			return w.Wrapf(err, "cannot wait for process to die")
		}
		return w.Wrapf(err, "cannot kill process")
	}
	return nil
}

func WrapStart(cmd *exec.Cmd, writers ...io.Writer) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "cannot create stdout pipe")
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return errors.Wrap(err, "cannot create stderr pipe")
	}

	var ws []io.Writer
	var errorWs []io.Writer
	for _, logger := range writers {
		ws = append(ws, logger)
		errorWs = append(errorWs, logger)
	}
	go ForwardLogs(stdout, ws...)

	//	catch the error
	var b bytes.Buffer
	w := bufio.NewWriter(&b)
	errorWs = append(errorWs, w)

	go ForwardLogs(stderr, errorWs...)

	err = cmd.Start()
	if err != nil {
		for _, writer := range writers {
			_, _ = writer.Write([]byte(err.Error()))
		}
		_ = w.Flush()
	}
	return nil
}

func WrapStartDebug(ctx context.Context, cmd *exec.Cmd, _ io.Writer) error {
	w := wool.Get(ctx).In("WrapStartDebug")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return w.Wrapf(err, "cannot start command: %s", out)
	}
	return nil
}

func ForwardLogs(r io.ReadCloser, ws ...io.Writer) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		t := scanner.Text()
		for _, w := range ws {
			_, _ = w.Write([]byte(t))
		}
	}

	if err := scanner.Err(); err != nil {
		for _, w := range ws {
			_, _ = w.Write([]byte(err.Error()))
		}
	}

	_ = r.Close()
}
