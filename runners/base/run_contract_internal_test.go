package base

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// exitFailure's contract when the process did not exit 0, for the one input
// Run's select cannot make deterministic: an exit observed on exitCh while the
// ctx is already cancelled. The branch answers ctx.Err(), as Run's ctx.Done
// arm does, so a cancelled Run reports the cancellation whichever arm saw it
// first; with a live ctx the same exit is the failure it is.
func TestExitFailureReportsACancelledContextWhicheverArmSawIt(t *testing.T) {
	exit := exec.Command("false").Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, exit, &exitErr)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	live := context.Background()

	native := &NativeProc{}
	require.ErrorIs(t, native.exitFailure(cancelled, exit), context.Canceled)
	require.True(t, errors.As(native.exitFailure(live, exit), &exitErr), "with a live context the exit is the failure it is")

	nix := &NixProc{}
	require.ErrorIs(t, nix.exitFailure(cancelled, exit), context.Canceled)
	require.True(t, errors.As(nix.exitFailure(live, exit), &exitErr))
}
