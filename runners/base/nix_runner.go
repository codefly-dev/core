package base

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/wool"
)

// NixEnvironment runs processes inside a Nix development shell.
// Every command is wrapped with `nix develop <dir> --command <bin> <args...>`.
// Binaries come from the flake.nix, so WithBinary is a no-op.
type NixEnvironment struct {
	dir       string
	flakePath string

	envs []*resources.EnvironmentVariable

	out io.Writer
	ctx context.Context
}

var _ RunnerEnvironment = &NixEnvironment{}

// NewNixEnvironment creates a new Nix runner.
// It verifies that nix is installed and that a flake.nix exists in dir.
func NewNixEnvironment(ctx context.Context, dir string) (*NixEnvironment, error) {
	w := wool.Get(ctx).In("NewNixEnvironment")

	if !CheckNixInstalled() {
		return nil, fmt.Errorf("nix is not installed (install with: %s)", NixInstallCommand())
	}

	flakePath := filepath.Join(dir, "flake.nix")
	if _, err := os.Stat(flakePath); err != nil {
		return nil, fmt.Errorf("no flake.nix found in %s: nix runtime requires a flake.nix", dir)
	}

	w.Info("using nix develop for reproducible environment", wool.DirField(dir))
	return &NixEnvironment{
		dir:       dir,
		flakePath: flakePath,
		out:       w,
	}, nil
}

func (nix *NixEnvironment) Init(ctx context.Context) error {
	nix.ctx = ctx
	return nil
}

func (nix *NixEnvironment) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	w := wool.Get(ctx).In("NixEnvironment.WithEnvironmentVariables")
	w.Debug("adding", wool.Field("envs", envs))
	nix.envs = append(nix.envs, envs...)
}

// WithBinary is a no-op for Nix -- all binaries come from the flake.
func (nix *NixEnvironment) WithBinary(_ string) error {
	return nil
}

func (nix *NixEnvironment) Stop(context.Context) error {
	return nil
}

func (nix *NixEnvironment) Shutdown(context.Context) error {
	return nil
}

// NewProcess creates a process that runs inside `nix develop --command`.
func (nix *NixEnvironment) NewProcess(bin string, args ...string) (Proc, error) {
	// nix develop <dir> --command <bin> <args...>
	cmd := []string{"nix", "develop", nix.dir, "--command", bin}
	cmd = append(cmd, args...)
	return &NixProc{
		env:     nix,
		cmd:     cmd,
		output:  nix.out,
		stopped: make(chan interface{}),
	}, nil
}

// --- NixProc ---

// NixProc is a process that runs inside a Nix development shell.
type NixProc struct {
	env    *NixEnvironment
	output io.Writer
	cmd    []string
	exec   *exec.Cmd
	envs   []*resources.EnvironmentVariable

	stopped chan interface{}

	dir    string
	waitOn string
}

func (proc *NixProc) WaitOn(bin string) {
	proc.waitOn = bin
}

func (proc *NixProc) WithDir(dir string) {
	proc.dir = dir
}

func (proc *NixProc) WithRunningCmd(_ string) {
}

func (proc *NixProc) WithOutput(output io.Writer) {
	proc.output = output
}

func (proc *NixProc) WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable) {
	w := wool.Get(ctx).In("NixProc.WithEnvironmentVariables")
	w.Debug("adding", wool.Field("envs", envs))
	proc.envs = append(proc.envs, envs...)
}

func (proc *NixProc) WithEnvironmentVariablesAppend(_ context.Context, added *resources.EnvironmentVariable, sep string) {
	for _, env := range proc.envs {
		if env.Key == added.Key {
			env.Value = fmt.Sprintf("%v%s%v", env.Value, sep, added.Value)
			return
		}
	}
	proc.envs = append(proc.envs, added)
}

func (proc *NixProc) IsRunning(ctx context.Context) (bool, error) {
	w := wool.Get(ctx).In("NixProc.IsRunning")
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
		return false, err
	}
	if strings.Contains(string(output), fmt.Sprintf("%d", pid)) &&
		!strings.Contains(string(output), "defunct") {
		return true, nil
	}
	return false, nil
}

func (proc *NixProc) Run(ctx context.Context) error {
	w := wool.Get(ctx).In("NixProc.Run")
	w.Debug("running nix process", wool.Field("cmd", proc.cmd))
	if err := proc.start(ctx); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- proc.exec.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			var exitError *exec.ExitError
			if errors.As(err, &exitError) {
				if strings.Contains(exitError.String(), "signal: terminated") {
					return nil
				}
				return exitError
			} else if strings.Contains(err.Error(), "signal: terminated") {
				return nil
			}
			return w.Wrapf(err, "nix process failed")
		}
	case <-proc.stopped:
		w.Debug("nix process was killed")
	}
	return nil
}

func (proc *NixProc) Start(ctx context.Context) error {
	w := wool.Get(ctx).In("NixProc.Start")
	w.Debug("starting nix process", wool.Field("cmd", proc.cmd))
	return proc.start(ctx)
}

func (proc *NixProc) start(ctx context.Context) error {
	w := wool.Get(ctx).In("NixProc.start", wool.DirField(proc.env.dir))
	// #nosec G204
	cmd := exec.CommandContext(ctx, proc.cmd[0], proc.cmd[1:]...)
	cmd.Dir = proc.env.dir
	if proc.dir != "" {
		cmd.Dir = proc.dir
	}
	cmd.Env = resources.EnvironmentVariableAsStrings(proc.env.envs)
	cmd.Env = append(cmd.Env, resources.EnvironmentVariableAsStrings(proc.envs)...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err = cmd.Start(); err != nil {
		return w.Wrapf(err, "cannot start nix process")
	}
	proc.exec = cmd

	go func() {
		defer stdout.Close()
		proc.forward(stdout)
	}()
	go func() {
		defer stderr.Close()
		proc.forward(stderr)
	}()
	w.Debug("nix process started")
	return nil
}

func (proc *NixProc) forward(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		line := scanner.Text()
		_, err := proc.output.Write([]byte(strings.TrimSpace(line)))
		if err != nil {
			_, _ = proc.output.Write([]byte(err.Error()))
			return
		}
	}
}

func (proc *NixProc) Stop(ctx context.Context) error {
	w := wool.Get(ctx).In("NixProc.Stop")
	w.Debug("stopping nix process")

	if err := proc.exec.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}
	time.Sleep(time.Second)

	if err := proc.exec.Process.Signal(syscall.Signal(0)); err == nil {
		w.Debug("nix process still alive after SIGTERM, sending SIGKILL")
		if killErr := proc.exec.Process.Kill(); killErr != nil {
			return fmt.Errorf("failed to force kill nix process: %w", killErr)
		}
	} else {
		w.Debug("nix process has exited after SIGTERM")
	}
	go func() {
		proc.stopped <- struct{}{}
	}()
	return nil
}
