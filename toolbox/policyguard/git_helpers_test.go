package policyguard_test

import (
	"context"
	"strings"

	"github.com/codefly-dev/core/internal/testgit"
)

func runGit(dir string, args ...string) error {
	if out, err := testgit.Run(context.Background(), dir, nil, args...); err != nil {
		return &gitErr{args: args, out: string(out), err: err}
	}
	return nil
}

func runGitWithEnv(dir string, args ...string) error {
	return runGit(dir, args...)
}

type gitErr struct {
	args []string
	out  string
	err  error
}

func (g *gitErr) Error() string {
	return "git " + strings.Join(g.args, " ") + ": " + g.err.Error() + "\n" + g.out
}
