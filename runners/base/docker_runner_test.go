package base_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/runners/base"
)

func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if !base.DockerEngineRunning(context.Background()) {
		t.Skip("Docker is not running; skipping test")
	}
}

func TestNewDockerEnvironment(t *testing.T) {
	skipIfNoDocker(t)
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

	// Check that the redis binary is there
	err = env.WithBinary("redis-server")
	require.NoError(t, err)

	err = env.WithBinary("nooooo")
	require.Error(t, err)

	timeout := time.NewTimer(3 * time.Second)

	for {
		select {
		case <-output.Signal():
			err = env.Stop(ctx)
			require.NoError(t, err)
			testOutput(t, d)
			return
		case <-timeout.C:
			// One second has passed
			t.Fatal("timeout")
		}

	}
}

func testOutput(t *testing.T, data *shared.SliceWriter) {
	// Data has been written to the output. Modern Redis (7.x) can emit
	// operator warnings (e.g. the Linux memory-overcommit nag) BEFORE
	// the startup banner, so the "Redis is starting" and "Redis version="
	// lines aren't guaranteed to land at indexes 0 and 1 anymore. Scan
	// the whole captured output instead of asserting positions.
	require.True(t, len(data.Data) > 1, "expected multiple log lines, got %d", len(data.Data))
	joined := strings.Join(data.Data, "\n")
	require.Contains(t, joined, "Redis is starting")
	require.Contains(t, joined, "Redis version=")
}

func TestDockerEnvironmentWithPauseAndProcesses(t *testing.T) {
	skipIfNoDocker(t)
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

// TestDockerProcStdinStdout verifies bidirectional pipe communication.
// It starts `cat` in a container, writes to StdinPipe, and reads back
// from StdoutPipe to confirm echo.
func TestDockerProcStdinStdout(t *testing.T) {
	skipIfNoDocker(t)
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	name := fmt.Sprintf("test-stdin-%d", time.Now().UnixMilli())
	env, err := base.NewDockerEnvironment(ctx, resources.NewDockerImage("alpine:3.19.1"), shared.Must(shared.SolvePath("testdata")), name)
	require.NoError(t, err)
	defer func() {
		_ = env.Shutdown(ctx)
	}()

	env.WithPause()
	err = env.Init(ctx)
	require.NoError(t, err)

	proc, err := env.NewProcess("cat")
	require.NoError(t, err)

	stdinW, err := proc.StdinPipe()
	require.NoError(t, err)

	stdoutR, err := proc.StdoutPipe()
	require.NoError(t, err)

	err = proc.Start(ctx)
	require.NoError(t, err)

	// Write a few lines to stdin; cat should echo them back on stdout.
	messages := []string{"hello world", "line two", "goodbye"}
	go func() {
		for _, msg := range messages {
			_, _ = fmt.Fprintf(stdinW, "%s\n", msg)
		}
		stdinW.Close()
	}()

	// Read back from stdout
	scanner := bufio.NewScanner(stdoutR)
	var received []string
	for scanner.Scan() {
		received = append(received, scanner.Text())
	}

	require.Equal(t, messages, received, "cat should echo back every line")
}

// TestDockerProcStdoutPipeRawBytes verifies that StdoutPipe delivers raw
// bytes without newline stripping or line-by-line processing.
func TestDockerProcStdoutPipeRawBytes(t *testing.T) {
	skipIfNoDocker(t)
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	name := fmt.Sprintf("test-raw-%d", time.Now().UnixMilli())
	env, err := base.NewDockerEnvironment(ctx, resources.NewDockerImage("alpine:3.19.1"), shared.Must(shared.SolvePath("testdata")), name)
	require.NoError(t, err)
	defer func() {
		_ = env.Shutdown(ctx)
	}()

	env.WithPause()
	err = env.Init(ctx)
	require.NoError(t, err)

	// printf outputs exactly the bytes we specify, including embedded newlines
	proc, err := env.NewProcess("sh", "-c", `printf 'line1\nline2\nline3\n'`)
	require.NoError(t, err)

	stdoutR, err := proc.StdoutPipe()
	require.NoError(t, err)

	// Use Start (not Run) so we can read stdout concurrently.
	err = proc.Start(ctx)
	require.NoError(t, err)

	all, err := io.ReadAll(stdoutR)
	require.NoError(t, err)

	// Verify raw bytes: newlines must be preserved
	require.Equal(t, "line1\nline2\nline3\n", string(all))
}

// TestDockerProcWithoutPipes verifies that existing WithOutput behaviour
// still works when pipes are not used (backward compatibility).
func TestDockerProcWithoutPipes(t *testing.T) {
	skipIfNoDocker(t)
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	name := fmt.Sprintf("test-nopp-%d", time.Now().UnixMilli())
	env, err := base.NewDockerEnvironment(ctx, resources.NewDockerImage("alpine:3.19.1"), shared.Must(shared.SolvePath("testdata")), name)
	require.NoError(t, err)
	defer func() {
		_ = env.Shutdown(ctx)
	}()

	env.WithPause()
	err = env.Init(ctx)
	require.NoError(t, err)

	proc, err := env.NewProcess("echo", "hello from docker")
	require.NoError(t, err)

	output := shared.NewSliceWriter()
	proc.WithOutput(output)

	err = proc.Run(ctx)
	require.NoError(t, err)

	require.Contains(t, output.Data, "hello from docker")
}

func TestFindFreePort(t *testing.T) {
	port, err := base.FindFreePort()
	require.NoError(t, err)
	require.Greater(t, port, 0)

	// The port should still be free right after (no listener)
	require.True(t, base.IsFreePort(port))
}
