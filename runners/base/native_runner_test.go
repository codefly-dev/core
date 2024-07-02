//go:build skip

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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	env, err := base.NewNativeEnvironment(ctx, shared.Must(shared.SolvePath("testdata")))
	require.NoError(t, err)

	err = env.Init(ctx)
	require.NoError(t, err)

	err = env.WithBinary("ls")
	require.NoError(t, err)

	proc, err := env.NewProcess("ls")
	require.NoError(t, err)

	d := shared.NewSliceWriter()
	output := shared.NewSignalWriter(d)
	proc.WithOutput(output)

	errCh := make(chan error, 1)
	go func() {
		errCh <- proc.Run(ctx)
	}()

	select {
	case <-output.Signal():
		// Data received, continue processing
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for output")
	case err := <-errCh:
		t.Fatalf("process exited unexpectedly: %v", err)
	}

	// Wait for the process to finish
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for process to finish")
	}

	d.Close() // Ensure any remaining data is flushed

	require.False(t, shared.Must(proc.IsRunning(ctx)))
	require.Contains(t, d.Data, "good")
	require.Contains(t, d.Data, "crashing")

}

func TestLocalEnvironmentFinite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	env, err := base.NewNativeEnvironment(ctx, shared.Must(shared.SolvePath("testdata")))
	require.NoError(t, err)

	err = env.Init(ctx)
	require.NoError(t, err)

	proc, err := env.NewProcess("sh", "good/finite_counter.sh")
	require.NoError(t, err)

	d := shared.NewSliceWriter()
	output := shared.NewSignalWriter(d)
	proc.WithOutput(output)

	errCh := make(chan error, 1)
	go func() {
		errCh <- proc.Run(ctx)
	}()

	select {
	case <-output.Signal():
		// Data received, continue processing
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for output")
	case err := <-errCh:
		t.Fatalf("process exited unexpectedly: %v", err)
	}

	// Wait for the process to finish
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for process to finish")
	}

	d.Close() // Ensure any remaining data is flushed

	require.False(t, shared.Must(proc.IsRunning(ctx)))
	require.Contains(t, d.Data, "1")
	t.Log("Output:", d.Data)
}

func TestLocalEnvironmentInfinite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	env, err := base.NewNativeEnvironment(ctx, shared.Must(shared.SolvePath("testdata")))
	require.NoError(t, err)

	err = env.Init(ctx)
	require.NoError(t, err)

	proc, err := env.NewProcess("sh", "good/infinite_counter.sh")
	require.NoError(t, err)

	d := shared.NewSliceWriter()
	output := shared.NewSignalWriter(d)
	proc.WithOutput(output)

	errCh := make(chan error, 1)
	go func() {
		errCh <- proc.Run(ctx)
	}()

	// Wait for initial output
	select {
	case <-output.Signal():
		// Data received, continue processing
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initial output")
	case err := <-errCh:
		t.Fatalf("process exited unexpectedly: %v", err)
	}

	// Stop the process after 3 seconds
	time.AfterFunc(3*time.Second, func() {
		err := proc.Stop(ctx)
		require.NoError(t, err)
	})

	// Wait for the process to finish
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for process to stop")
	}

	d.Close() // Ensure any remaining data is flushed

	require.Contains(t, d.Data, "1")
	err = env.Shutdown(ctx)
	require.NoError(t, err)
}

func TestCrashing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	env, err := base.NewNativeEnvironment(ctx, shared.Must(shared.SolvePath("testdata")))
	require.NoError(t, err)

	err = env.Init(ctx)
	require.NoError(t, err)

	t.Run("NonExistentScript", func(t *testing.T) {
		proc, err := env.NewProcess("sh", "not_there.sh")
		require.NoError(t, err)

		err = proc.Run(ctx)
		require.Error(t, err)
	})

	t.Run("CrashingScript", func(t *testing.T) {
		proc, err := env.NewProcess("sh", "crashing/crash.sh")
		require.NoError(t, err)

		err = proc.Run(ctx)
		require.Error(t, err)
	})

	err = env.Shutdown(ctx)
	require.NoError(t, err)
}
