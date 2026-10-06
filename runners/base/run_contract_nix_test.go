//go:build !skip_infra

package base_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/codefly-dev/core/runners/base"
	"github.com/stretchr/testify/require"
)

// The Nix runner carries the same Run contract as the native one: the nix
// process and everything under it share one process group, so a SIGTERM to
// that group from inside is a termination nothing in this Proc requested, and
// Run reports it instead of reading it as success.
func TestNixProc_Run_SignalFromOutsideStopIsAFailure(t *testing.T) {
	requireNix(t)
	ctx := context.Background()
	env, err := base.NewNixEnvironment(ctx, nixTestDir(t))
	require.NoError(t, err, "nix environment not usable")
	require.NoError(t, env.Init(ctx))

	proc, err := env.NewProcess("sh", "-c", terminateOwnGroup)
	require.NoError(t, err)
	err = proc.Run(ctx)
	require.Error(t, err, "a nix process terminated by a signal did not finish")
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Contains(t, exitErr.String(), "signal: terminated")
}
