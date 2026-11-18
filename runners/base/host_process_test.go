package base_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/runners/base"
	"github.com/stretchr/testify/require"
)

// A store that starts its server through a Proc records that server's birth
// (runstate's Hold.Track) from the handle it already holds.
func TestNativeProcReportsTheProcessItStarted(t *testing.T) {
	ctx := context.Background()
	env, err := base.NewNativeEnvironment(ctx, t.TempDir())
	require.NoError(t, err)
	proc, err := env.NewProcess("sleep", "30")
	require.NoError(t, err)
	host, ok := proc.(base.HostProcess)
	require.True(t, ok, "a native Proc is a process of this machine")

	_, err = host.Process()
	require.ErrorIs(t, err, base.ErrProcessNotFound, "nothing is started yet")

	require.NoError(t, proc.Start(ctx))
	t.Cleanup(func() { _ = proc.Stop(context.Background()) })

	identity, err := host.Process()
	require.NoError(t, err)
	require.Greater(t, identity.PID, 1)
	live, err := base.InspectProcess(identity.PID)
	require.NoError(t, err)
	require.Equal(t, live.StartID, identity.StartID)
	require.Equal(t, live.BootID, identity.BootID)
	require.NotZero(t, identity.StartID)
}
