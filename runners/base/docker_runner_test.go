//go:build !skip

package base_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/runners/base"
)

func TestNewDockerEnvironment(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	name := fmt.Sprintf("test-%d", time.Now().UnixMilli())
	env, err := base.NewDockerHeadlessEnvironment(ctx, resources.NewDockerImage("redis:7.2.4-alpine"), name)
	require.NoError(t, err)

	defer func() {
		err = env.Shutdown(ctx)
		require.NoError(t, err)
		deleted, err := env.ContainerDeleted()
		require.NoError(t, err)
		require.True(t, deleted)
	}()

	d := shared.NewSliceWriter()
	output := shared.NewSignalWriter(d)

	env.WithOutput(output)

	err = env.Init(ctx)
	require.NoError(t, err)

	timeout := time.NewTimer(10 * time.Second)

	for {
		select {
		case <-output.Signal():
			err = env.Stop(ctx)
			require.NoError(t, err)
			testOutput(t, d)
			return
		case <-timeout.C:
			t.Fatal("timeout")
		}

	}
}

func TestNewDockerEnvironmentBinaries(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	name := fmt.Sprintf("test-%d", time.Now().UnixMilli())
	env, err := base.NewDockerHeadlessEnvironment(ctx, resources.NewDockerImage("redis:7.2.4-alpine"), name)
	require.NoError(t, err)
	env.WithPause()
	defer func() {
		err = env.Shutdown(ctx)
		require.NoError(t, err)
		deleted, err := env.ContainerDeleted()
		require.NoError(t, err)
		require.True(t, deleted)
	}()

	err = env.Init(ctx)
	require.NoError(t, err)

	// Check that the redis binary is there
	err = env.WithBinary("redis-server")
	require.NoError(t, err)

	err = env.WithBinary("nooooo")
	require.Error(t, err)

}

func testOutput(t *testing.T, data *shared.SliceWriter) {
	// Data has been written to the output
	require.True(t, len(data.Data) > 1)
	require.Contains(t, data.Data[0], "Redis is starting")
	require.Contains(t, data.Data[1], "Redis version=")
}

func TestDockerEnvironmentWithPauseAndProcesses(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	name := fmt.Sprintf("test-%d", time.Now().UnixMilli())
	env, err := base.NewDockerEnvironment(ctx, resources.NewDockerImage("alpine:3.19.1"), shared.Must(shared.SolvePath("testdata")), name)
	require.NoError(t, err)

	defer func() {
		err = env.Shutdown(ctx)
		require.NoError(t, err)
	}()

	env.WithPause()

	// Nothing created yet
	_, err = env.ContainerID()
	require.Error(t, err)

	err = env.Init(ctx)
	require.NoError(t, err)

	// Nothing created yet
	_, err = env.ContainerID()
	require.NoError(t, err)

	// Now, run something in it
	proc, err := env.NewProcess("ls")
	require.NoError(t, err)

	output := shared.NewSliceWriter()
	proc.WithOutput(output)

	err = proc.Run(ctx)
	require.NoError(t, err)

	require.False(t, shared.Must(proc.IsRunning(ctx)))

	// We should have an ID now
	id, err := env.ContainerID()
	require.NoError(t, err)

	require.Contains(t, output.Data, "good")
	require.Contains(t, output.Data, "crashing")

	id2, err := env.ContainerID()
	require.NoError(t, err)
	require.Equal(t, id, id2)

	// Now, run something in it
	proc, err = env.NewProcess("ls")
	require.NoError(t, err)
	output = shared.NewSliceWriter()
	proc.WithOutput(output)

	err = proc.Run(ctx)
	require.NoError(t, err)
	require.False(t, shared.Must(proc.IsRunning(ctx)))

	require.Contains(t, output.Data, "good")
	require.Contains(t, output.Data, "crashing")

	// Run a finite script
	proc, err = env.NewProcess("sh", "good/finite_counter.sh")
	require.NoError(t, err)
	output = shared.NewSliceWriter()
	proc.WithOutput(output)

	err = proc.Run(ctx)
	require.NoError(t, err)
	require.Contains(t, output.Data, "1")

	require.False(t, shared.Must(proc.IsRunning(ctx)))

	// Run an infinite script and stop it after 2 seconds
	proc, err = env.NewProcess("sh", "good/infinite_counter.sh")
	require.NoError(t, err)
	output = shared.NewSliceWriter()
	proc.WithOutput(output)

	err = proc.Start(ctx)
	require.NoError(t, err)

	require.True(t, shared.Must(proc.IsRunning(ctx)))

	wait := time.NewTimer(time.Second)
	<-wait.C
	err = proc.Stop(ctx)
	require.NoError(t, err)

	require.Contains(t, output.Data, "1")

	proc, err = env.NewProcess("sh", "good/finite_counter.sh")
	require.NoError(t, err)
	output = shared.NewSliceWriter()
	proc.WithOutput(output)

	err = proc.Run(ctx)
	require.NoError(t, err)
	require.Contains(t, output.Data, "1")

	require.False(t, shared.Must(proc.IsRunning(ctx)))
}
