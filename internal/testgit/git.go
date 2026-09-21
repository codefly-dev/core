// Package testgit runs isolated, bounded Git fixture commands.
package testgit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/codefly-dev/core/runners/base"
)

func Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command := exec.Command("git", append([]string{
		"-c", "commit.gpgSign=false", "-c", "tag.gpgSign=false",
		"-c", "core.hooksPath=" + os.DevNull,
	}, args...)...)
	command.Dir = dir
	command.Env = append(command.Environ(),
		"GIT_AUTHOR_NAME=codefly", "GIT_AUTHOR_EMAIL=test@codefly.dev",
		"GIT_COMMITTER_NAME=codefly", "GIT_COMMITTER_EMAIL=test@codefly.dev",
		"GIT_EDITOR=false", "GIT_SEQUENCE_EDITOR=false", "GIT_TERMINAL_PROMPT=0",
	)
	command.Env = append(command.Env, env...)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	command.WaitDelay = time.Second
	group, err := base.StartOwnedProcessGroup(command)
	if err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	var waitErr error
	waited := false
	select {
	case waitErr = <-done:
		waited = true
	case <-ctx.Done():
	}
	cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	cleanupErr := group.Terminate(cleanup, 0)
	if !waited {
		waitErr = <-done
	}
	return output.Bytes(), errors.Join(waitErr, ctx.Err(), cleanupErr)
}
