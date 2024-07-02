package base

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

type NativeEnvironment struct {
	dir string

	envs []*resources.EnvironmentVariable

	out io.Writer

	ctx context.Context
}

var _ RunnerEnvironment = &NativeEnvironment{}

// NewNativeEnvironment creates a new docker runner
func NewNativeEnvironment(ctx context.Context, dir string) (*NativeEnvironment, error) {
	w := wool.Get(ctx).In("NewDockerRunner")
	return &NativeEnvironment{
		out: w,
		dir: dir,
	}, nil
}

func (native *NativeEnvironment) Init(ctx context.Context) error {
	native.ctx = ctx
	return nil
}

func (native *NativeEnvironment) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	w := wool.Get(ctx).In("WithEnvironmentVariables")
	w.Debug("adding", wool.Field("envs", envs))
	native.envs = append(native.envs, envs...)
}

func (native *NativeEnvironment) WithBinary(bin string) error {
	p, err := exec.LookPath(bin)
	if err != nil {
		return err
	}
	// Get the PATH environment variable
	for _, env := range native.envs {
		if env.Key == "PATH" {
			env.Value = fmt.Sprintf("%s:%s", env.Value, filepath.Dir(p))
			return nil
		}
	}
	native.envs = append(native.envs, &resources.EnvironmentVariable{Key: "PATH", Value: filepath.Dir(p)})
	return nil
}

func (native *NativeEnvironment) Shutdown(context.Context) error {
	return nil
}

/*
Proc
*/

type NativeProc struct {
	env    *NativeEnvironment
	output io.Writer
	cmd    []string
	exec   *exec.Cmd
	envs   []*resources.EnvironmentVariable

	stopped chan interface{}

	// optional override
	dir    string
	waitOn string
}

// IsRunning checks if the exec is still running
func (proc *NativeProc) IsRunning(ctx context.Context) (bool, error) {
	w := wool.Get(ctx).In("NativeProc.IsRunning")
	// Check the PID
	if proc.exec == nil || proc.exec.Process == nil {
		return false, nil
	}
	pid := proc.exec.Process.Pid
	w.Debug("checking if process is running", wool.Field("pid", pid))
	// #nosec G204
	cmd := exec.Command("ps", "-p", fmt.Sprintf("%d", pid))
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(err.Error(), "exit") {
			return false, nil
		}
		w.Debug("error checking if process is running", wool.Field("error", err), wool.Field("output", string(output)))
		return false, err
	}
	w.Debug("process is running", wool.Field("output", string(output)))
	if strings.Contains(string(output), fmt.Sprintf("%d", pid)) &&
		!strings.Contains(string(output), "defunct") {
		return true, nil
	}
	return false, nil
}

func (proc *NativeProc) WaitOn(bin string) {
	proc.waitOn = bin
}

func (proc *NativeProc) WithDir(dir string) {
	proc.dir = dir
}

func (proc *NativeProc) WithRunningCmd(_ string) {
}

func (proc *NativeProc) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	w := wool.Get(ctx).In("WithEnvironmentVariables")
	w.Debug("adding", wool.Field("envs", envs))
	proc.envs = append(proc.envs, envs...)
}

func (native *NativeEnvironment) NewProcess(bin string, args ...string) (Proc, error) {
	if _, err := exec.LookPath(bin); err != nil {
		return nil, err
	}
	cmd := append([]string{bin}, args...)
	return &NativeProc{
		env:     native,
		cmd:     cmd,
		output:  native.out,
		stopped: make(chan interface{})}, nil
}

func (native *NativeEnvironment) Stop(context.Context) error {
	return nil
}
func (proc *NativeProc) Run(ctx context.Context) error {
	w := wool.Get(ctx).In("NativeProc.Run")
	w.Debug("running process", wool.Field("cmd", proc.cmd), wool.Field("envs", proc.env.envs))

	if err := proc.start(ctx); err != nil {
		return w.Wrapf(err, "failed to start process")
	}

	w.Debug("waiting for process to finish or be killed")

	done := make(chan error, 1)
	go func() {
		done <- proc.exec.Wait()
	}()

	select {
	case <-ctx.Done():
		w.Debug("context cancelled, stopping process")
		if err := proc.Stop(ctx); err != nil {
			w.Debug("error stopping process", wool.Field("error", err))
		}
		return ctx.Err()
	case err := <-done:
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				if exitErr.ExitCode() == -1 && strings.Contains(exitErr.Error(), "signal: terminated") {
					w.Debug("process was terminated")
					return nil
				}
				return w.Wrapf(exitErr, "process exited with non-zero status: %d", exitErr.ExitCode())
			}
			return w.Wrapf(err, "error waiting for process")
		}
	case <-proc.stopped:
		w.Debug("process was manually stopped")
	}

	w.Debug("process finished")
	return nil
}

func (proc *NativeProc) Start(ctx context.Context) error {
	w := wool.Get(ctx).In("NativeProc.Start")
	w.Debug("starting process", wool.Field("cmd", proc.cmd), wool.Field("envs", proc.env.envs))
	return proc.start(ctx)

}
func (proc *NativeProc) start(ctx context.Context) error {
	w := wool.Get(ctx).In("NativeProc.start", wool.DirField(proc.env.dir))

	cmd := exec.CommandContext(ctx, proc.cmd[0], proc.cmd[1:]...)
	cmd.Dir = proc.env.dir
	if proc.dir != "" {
		cmd.Dir = proc.dir
	}
	cmd.Env = append(os.Environ(), resources.EnvironmentVariableAsStrings(proc.env.envs)...)
	cmd.Env = append(cmd.Env, resources.EnvironmentVariableAsStrings(proc.envs)...)

	w.Debug("envs", wool.Field("envs", cmd.Env))

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return w.Wrapf(err, "failed to create stdout pipe")
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return w.Wrapf(err, "failed to create stderr pipe")
	}

	if err := cmd.Start(); err != nil {
		return w.Wrapf(err, "failed to start command")
	}
	proc.exec = cmd

	go proc.Forward(ctx, stdout)
	go proc.Forward(ctx, stderr)

	w.Debug("process started")
	return nil
}

func (proc *NativeProc) Forward(ctx context.Context, r io.Reader) {
	w := wool.Get(ctx).In("NativeProc.Forward")
	scanner := bufio.NewScanner(r)
	scanner.Split(bufio.ScanLines)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			w.Debug("context cancelled, stopping log forwarding")
			return
		default:
			line := strings.TrimSpace(scanner.Text())
			if line != "" { // Only write non-empty lines
				if _, err := fmt.Fprintln(proc.output, line); err != nil {
					w.Error("failed to write log line", wool.Field("error", err))
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		w.Error("error scanning logs", wool.Field("error", err))
	}
}

func (proc *NativeProc) WithOutput(output io.Writer) {
	proc.output = output
}

func (proc *NativeProc) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("NativeProc.Stop")
	w.Debug("stopping process")

	if proc.exec == nil || proc.exec.Process == nil {
		w.Debug("process not running")
		return nil
	}

	// Send SIGTERM
	if err := proc.exec.Process.Signal(syscall.SIGTERM); err != nil {
		w.Debug("failed to send SIGTERM", wool.Field("error", err))
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}

	// Wait for process to exit with a timeout
	gracefulShutdownTimeout := 5 * time.Second
	timer := time.NewTimer(gracefulShutdownTimeout)
	defer timer.Stop()

	done := make(chan error, 1)
	go func() {
		_, err := proc.exec.Process.Wait()
		done <- err
	}()

	select {
	case <-timer.C:
		w.Debug("graceful shutdown timed out, force killing")
		if err := proc.exec.Process.Kill(); err != nil {
			w.Debug("failed to force kill process", wool.Field("error", err))
			return fmt.Errorf("failed to force kill process: %w", err)
		}
	case err := <-done:
		if err != nil {
			w.Debug("process exited with error", wool.Field("error", err))
		} else {
			w.Debug("process exited gracefully")
		}
	}

	close(proc.stopped)
	return nil
}
