package base_test

import (
	"context"
	"testing"
	"time"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/runners/base"
	"github.com/stretchr/testify/require"
)

func TestLocalEnvironmentLs(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	env, err := base.NewNativeEnvironment(ctx, shared.Must(shared.SolvePath("testdata")))
	require.NoError(t, err)

	err = env.Init(ctx)
	require.NoError(t, err)

	// Check that the ls binary is there
	err = env.WithBinary("ls")
	require.NoError(t, err)

	// Now, run something in it
	proc, err := env.NewProcess("ls")
	require.NoError(t, err)

	d := shared.NewSliceWriter()
	output := shared.NewSignalWriter(d)
	proc.WithOutput(output)

	err = proc.Run(ctx)
	require.NoError(t, err)

	timeout := time.NewTimer(3 * time.Second)
loop:
	for {
		select {
		case <-output.Signal():
			break loop
		case <-timeout.C:
			t.Log("OUTPUT", d.Data)
			t.Fatal("timeout")
		}

	}
	time.Sleep(500 * time.Millisecond)
	require.False(t, shared.Must(proc.IsRunning(ctx)))
	require.Contains(t, d.Data, "good")
	require.Contains(t, d.Data, "crashing")

}

func TestLocalEnvironmentFinite(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	env, err := base.NewNativeEnvironment(ctx, shared.Must(shared.SolvePath("testdata")))
	require.NoError(t, err)

	err = env.Init(ctx)
	require.NoError(t, err)

	// Run a finite script
	proc, err := env.NewProcess("sh", "good/finite_counter.sh")
	require.NoError(t, err)

	d := shared.NewSliceWriter()
	output := shared.NewSignalWriter(d)
	proc.WithOutput(output)

	err = proc.Run(ctx)
	require.NoError(t, err)
	timeout := time.NewTimer(5 * time.Second)
loopFirst:
	for {
		select {
		case <-output.Signal():
			break loopFirst
		case <-timeout.C:
			// One second has passed
			t.Fatal("timeout")
		}
	}
	time.Sleep(500 * time.Millisecond)
	require.False(t, shared.Must(proc.IsRunning(ctx)))
	require.Contains(t, d.Data, "1")

}

func TestLocalEnvironment(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	env, err := base.NewNativeEnvironment(ctx, shared.Must(shared.SolvePath("testdata")))
	require.NoError(t, err)

	err = env.Init(ctx)
	require.NoError(t, err)

	// Run an infinite script and stop it after a bit
	proc, err := env.NewProcess("sh", "good/infinite_counter.sh")
	require.NoError(t, err)

	d := shared.NewSliceWriter()
	output := shared.NewSignalWriter(d)
	proc.WithOutput(output)

	go func() {
		wait := time.NewTimer(3 * time.Second)
		<-wait.C
		err := proc.Stop(ctx)
		require.NoError(t, err)
	}()

	err = proc.Run(ctx)
	require.NoError(t, err)

	timeout := time.NewTimer(5 * time.Second)

loopLastTime:
	for {
		select {
		case <-output.Signal():
			break loopLastTime
		case <-timeout.C:
			t.Fatal("timeout")
		}

	}
	time.Sleep(500 * time.Millisecond)
	require.Contains(t, d.Data, "1")

	err = env.Shutdown(ctx)
	require.NoError(t, err)
}

func TestCrashing(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	env, err := base.NewNativeEnvironment(ctx, shared.Must(shared.SolvePath("testdata")))
	require.NoError(t, err)

	err = env.Init(ctx)
	require.NoError(t, err)

	proc, err := env.NewProcess("sh", "not_there.sh")
	require.NoError(t, err)

	err = proc.Run(ctx)
	require.Error(t, err)

	proc, err = env.NewProcess("sh", "crashing/crash.sh")
	require.NoError(t, err)

	err = proc.Run(ctx)
	require.Error(t, err)
}
