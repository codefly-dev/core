package base

import (
	"bytes"
	"context"
	"io"
)

// RunInput executes one bounded tool invocation with source on stdin, source
// output captured separately from diagnostics, and the requested working
// directory. It works across native, Nix, and Docker RunnerEnvironments and is
// the preferred building block for in-memory language fixers.
func RunInput(ctx context.Context, env RunnerEnvironment, dir string, input []byte, command string, args ...string) (stdoutBytes, stderrBytes []byte, runErr error) {
	proc, err := env.NewProcess(command, args...)
	if err != nil {
		return nil, nil, err
	}
	proc.WithDir(dir)
	var stderr bytes.Buffer
	proc.WithOutput(&stderr)
	stdin, err := proc.StdinPipe()
	if err != nil {
		return nil, stderr.Bytes(), err
	}
	stdout, err := proc.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, stderr.Bytes(), err
	}
	if err := proc.Start(ctx); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, stderr.Bytes(), err
	}
	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := stdin.Write(input)
		closeErr := stdin.Close()
		if writeErr != nil {
			writeDone <- writeErr
			return
		}
		writeDone <- closeErr
	}()
	result, readErr := io.ReadAll(stdout)
	_ = stdout.Close()
	waitErr := proc.Wait(ctx)
	writeErr := <-writeDone
	if writeErr != nil {
		return nil, stderr.Bytes(), writeErr
	}
	if readErr != nil {
		return nil, stderr.Bytes(), readErr
	}
	return result, stderr.Bytes(), waitErr
}
