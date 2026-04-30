package policyguard_test

import (
	"os"
	"os/exec"
	"strings"
)

// Local git helpers — duplicated across toolbox tests to keep each
// package self-contained without a shared internal/testutil import.

func runGit(dir string, args ...string) error {
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		return &gitErr{args: args, out: string(out), err: err}
	}
	return nil
}

func runGitWithEnv(dir string, args ...string) error {
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := c.CombinedOutput(); err != nil {
		return &gitErr{args: args, out: string(out), err: err}
	}
	return nil
}

type gitErr struct {
	args []string
	out  string
	err  error
}

func (g *gitErr) Error() string {
	return "git " + strings.Join(g.args, " ") + ": " + g.err.Error() + "\n" + g.out
}
