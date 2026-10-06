package base_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/codefly-dev/core/runners/base"
	"github.com/stretchr/testify/require"
)

// Run's contract: nil only for an exit status of 0 or a termination this
// Proc's own Stop requested. A signal from anywhere else means the process
// did not finish — it used to be read as success whenever the signal was
// SIGTERM, which let an interrupted build publish the previous executable.

func nativeProc(t *testing.T, ctx context.Context, args ...string) base.Proc {
	t.Helper()
	env, err := base.NewNativeEnvironment(ctx, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, env.Init(ctx))
	proc, err := env.NewProcess(args[0], args[1:]...)
	require.NoError(t, err)
	return proc
}

// Every child runs in its own process group, so `kill -TERM 0` from inside
// is a SIGTERM to exactly this Proc's group from something other than Stop.
const terminateOwnGroup = "kill -TERM 0"

func TestNativeProc_Run_SignalFromOutsideStopIsAFailure(t *testing.T) {
	ctx := context.Background()
	proc := nativeProc(t, ctx, "sh", "-c", terminateOwnGroup)
	err := proc.Run(ctx)
	require.Error(t, err, "a process terminated by a signal did not finish")
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Contains(t, exitErr.String(), "signal: terminated")
}

// runInBackground starts Run and returns its result channel once the process
// is observably running, so a Stop or a cancel lands on a live process.
func runInBackground(t *testing.T, ctx context.Context, proc base.Proc) <-chan error {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- proc.Run(ctx) }()
	require.Eventually(t, func() bool {
		running, err := proc.IsRunning(context.Background())
		return err == nil && running
	}, 10*time.Second, 20*time.Millisecond, "process never reported running")
	return result
}

func TestNativeProc_Run_RequestedStopIsNotAFailure(t *testing.T) {
	ctx := context.Background()
	proc := nativeProc(t, ctx, "sleep", "30")
	result := runInBackground(t, ctx, proc)
	require.NoError(t, proc.Stop(ctx))
	require.NoError(t, <-result, "a stop this Proc requested is the one termination that is not a failure")
}

func TestNativeProc_Run_CancelledContextIsReportedAsSuch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proc := nativeProc(t, ctx, "sleep", "30")
	result := runInBackground(t, ctx, proc)
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
}
