// Package testgit runs isolated, bounded Git fixture commands.
package testgit

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{
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
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = time.Second
	return command.CombinedOutput()
}
