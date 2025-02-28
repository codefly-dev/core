package base

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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

func (proc *NativeProc) WithEnvironmentVariablesAppend(ctx context.Context, added *resources.EnvironmentVariable, sep string) {
	for _, env := range proc.envs {
		if env.Key == env.Key {
			env.Value = fmt.Sprintf("%v%s%v", env.Value, sep, added.Value)
			return
		}
	}
	proc.envs = append(proc.envs, added)
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
	err := proc.start(ctx)
	if err != nil {
		return err
	}
	w.Debug("waiting for process to finish or be killed")
	// TODO: handle waitOn
	// Create a channel to receive the result of proc.exec.Wait()
	done := make(chan error, 1)
	go func() {
		done <- proc.exec.Wait()
	}()

	// Use a select statement to wait for either the process to finish or the context to be cancelled
	select {
	case err := <-done:
		if err != nil {
			var exitError *exec.ExitError
			if errors.As(err, &exitError) {
				// The program has exited with an exit code != 0
				if strings.Contains(exitError.String(), "signal: terminated") {
					return nil
				}
				return exitError
			} else if strings.Contains(err.Error(), "signal: terminated") {
				return nil
			}
			return w.Wrapf(err, "cannot wait for process")
		}
	case <-proc.stopped:
		w.Debug("process was killed")
	}
	w.Debug("done")
	return nil
}

func (proc *NativeProc) Start(ctx context.Context) error {
	w := wool.Get(ctx).In("NativeProc.Start")
	w.Debug("starting process", wool.Field("cmd", proc.cmd), wool.Field("envs", proc.env.envs))
	return proc.start(ctx)

}
func (proc *NativeProc) start(ctx context.Context) error {
	w := wool.Get(ctx).In("NativeProc.start", wool.DirField(proc.env.dir))
	// #nosec G204
	cmd := exec.CommandContext(ctx, proc.cmd[0], proc.cmd[1:]...)
	cmd.Dir = proc.env.dir
	if proc.dir != "" {
		cmd.Dir = proc.dir
	}
	cmd.Env = resources.EnvironmentVariableAsStrings(proc.env.envs)
	cmd.Env = append(cmd.Env, resources.EnvironmentVariableAsStrings(proc.envs)...)
	w.Debug("envs", wool.Field("envs", cmd.Env))

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

func (proc *NativeProc) Forward(_ context.Context, w io.Reader) {
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

func (proc *NativeProc) WithOutput(output io.Writer) {
	proc.output = output
}

func (proc *NativeProc) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("NativeProc.Stop")
	w.Debug("stopping process")

	// Attempt to gracefully terminate the process
	w.Debug("sending SIGTERM to process")
	err := proc.exec.Process.Signal(syscall.SIGTERM)
	if err != nil {
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}
	time.Sleep(time.Second)

	// Check if the process has exited
	if err := proc.exec.Process.Signal(syscall.Signal(0)); err == nil {
		w.Debug("process is still alive after SIGTERM, sending SIGKILL")
		// Process is still alive after SIGTERM and waiting period, force kill
		if killErr := proc.exec.Process.Kill(); killErr != nil {
			w.Debug("failed to force kill process", wool.Field("error", killErr))
			return fmt.Errorf("failed to force kill process: %w", killErr)
		}
	} else {
		// Process has exited, or an error occurred when checking the process status
		w.Debug("process has exited after SIGTERM")
		if !strings.Contains(err.Error(), "process already finished") {
			// Handle or log this error if it's not the expected "already finished" error
			w.Debug("error checking process status", wool.Field("error", err))
		}
	}
	go func() {
		proc.stopped <- struct{}{}
	}()
	return nil

}
