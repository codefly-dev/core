package proto

import (
	"context"
	"fmt"
	"os/user"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/companion"
)

// configureBindMountOwnership makes Docker companions write generated files as
// the invoking host user. The explicit HOME keeps tools functional when the
// numeric host identity does not have a passwd entry inside the image.
func configureBindMountOwnership(ctx context.Context, runner companion.CompanionRunner) error {
	if runner.Backend() != companion.BackendDocker {
		return nil
	}

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("resolve host identity: %w", err)
	}
	if currentUser.Uid == "" || currentUser.Gid == "" {
		return fmt.Errorf("host identity has empty uid or gid")
	}

	runner.WithUser(currentUser.Uid + ":" + currentUser.Gid)
	runner.RunnerEnv().WithEnvironmentVariables(ctx, resources.Env("HOME", "/tmp"))
	return nil
}
