//go:build !skip_infra

package dockerrun_test

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
	"github.com/codefly-dev/core/runners/dockerrun"
)

func requireDocker(t *testing.T) {
	t.Helper()
	if !dockerrun.DockerEngineRunning(context.Background()) {
		t.Fatal("Docker is not running; bring it up or run with -tags skip_infra to exclude")
	}
}

func TestNewDockerEnvironment(t *testing.T) {
	requireDocker(t)
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	name := fmt.Sprintf("test-%d", time.Now().UnixMilli())
	env, err := dockerrun.NewDockerHeadlessEnvironment(ctx, resources.NewDockerImage("redis:7.2.4-alpine"), name)
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
	//
	// Snapshot under lock — the docker stdcopy goroutine may still be
	// draining the last buffered line when Stop returns.
	snap := data.Snapshot()
	require.True(t, len(snap) > 1, "expected multiple log lines, got %d", len(snap))
	joined := strings.Join(snap, "\n")
	require.Contains(t, joined, "Redis is starting")
	require.Contains(t, joined, "Redis version=")
}

func TestDockerEnvironmentWithPauseAndProcesses(t *testing.T) {
	requireDocker(t)
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	name := fmt.Sprintf("test-%d", time.Now().UnixMilli())
	env, err := dockerrun.NewDockerEnvironment(ctx, resources.NewDockerImage("alpine:3.19.1"), shared.Must(shared.SolvePath("testdata")), name)
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

	require.Contains(t, output.Snapshot(), "good")
	require.Contains(t, output.Snapshot(), "crashing")

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

	require.Contains(t, output.Snapshot(), "good")
	require.Contains(t, output.Snapshot(), "crashing")

	// Run a finite script
	proc, err = env.NewProcess("sh", "good/finite_counter.sh")
	require.NoError(t, err)
	output = shared.NewSliceWriter()
	proc.WithOutput(output)

	err = proc.Run(ctx)
	require.NoError(t, err)
	require.Contains(t, output.Snapshot(), "1")

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

	// Use Snapshot() — the docker stdcopy goroutine may still be draining
	// the last buffered line when Stop returns. Reading Data directly
	// races with SliceWriter.Write under -race.
	require.Contains(t, output.Snapshot(), "1")

	proc, err = env.NewProcess("sh", "good/finite_counter.sh")
	require.NoError(t, err)
	output = shared.NewSliceWriter()
	proc.WithOutput(output)

	err = proc.Run(ctx)
	require.NoError(t, err)
	require.Contains(t, output.Snapshot(), "1")

	require.False(t, shared.Must(proc.IsRunning(ctx)))
}

// TestDockerProcStdinStdout verifies bidirectional pipe communication.
// It starts `cat` in a container, writes to StdinPipe, and reads back
// from StdoutPipe to confirm echo.
func TestDockerProcStdinStdout(t *testing.T) {
	requireDocker(t)
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	name := fmt.Sprintf("test-stdin-%d", time.Now().UnixMilli())
	env, err := dockerrun.NewDockerEnvironment(ctx, resources.NewDockerImage("alpine:3.19.1"), shared.Must(shared.SolvePath("testdata")), name)
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
	requireDocker(t)
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	name := fmt.Sprintf("test-raw-%d", time.Now().UnixMilli())
	env, err := dockerrun.NewDockerEnvironment(ctx, resources.NewDockerImage("alpine:3.19.1"), shared.Must(shared.SolvePath("testdata")), name)
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
	requireDocker(t)
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	name := fmt.Sprintf("test-nopp-%d", time.Now().UnixMilli())
	env, err := dockerrun.NewDockerEnvironment(ctx, resources.NewDockerImage("alpine:3.19.1"), shared.Must(shared.SolvePath("testdata")), name)
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

	require.Contains(t, output.Snapshot(), "hello from docker")
}

// TestDockerProcWaitReturnsExitError verifies Wait honors the Proc.Wait
// contract: nil for a clean exit, non-nil for a non-zero exit. The previous
// implementation polled IsRunning and could only ever return nil, so a
// supervisor Waiting on a crashed exec saw a clean exit.
func TestDockerProcWaitReturnsExitError(t *testing.T) {
	requireDocker(t)
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	name := fmt.Sprintf("test-wait-%d", time.Now().UnixMilli())
	env, err := dockerrun.NewDockerEnvironment(ctx, resources.NewDockerImage("alpine:3.19.1"), shared.Must(shared.SolvePath("testdata")), name)
	require.NoError(t, err)
	defer func() {
		_ = env.Shutdown(ctx)
	}()

	env.WithPause()
	require.NoError(t, env.Init(ctx))

	// Clean exit: Wait returns nil.
	clean, err := env.NewProcess("sh", "-c", "exit 0")
	require.NoError(t, err)
	require.NoError(t, clean.Start(ctx))
	require.NoError(t, clean.Wait(ctx))

	// Non-zero exit: Wait must surface the failure.
	crashing, err := env.NewProcess("sh", "-c", "exit 3")
	require.NoError(t, err)
	require.NoError(t, crashing.Start(ctx))
	require.Error(t, crashing.Wait(ctx))

	// ctx cancellation short-circuits a still-running exec.
	longRun, err := env.NewProcess("sh", "-c", "sleep 30")
	require.NoError(t, err)
	require.NoError(t, longRun.Start(ctx))
	cancelCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	require.ErrorIs(t, longRun.Wait(cancelCtx), context.DeadlineExceeded)
	require.NoError(t, longRun.Stop(ctx))
}

// TestDockerProcRunCancellation runs a never-ending in-container command
// and cancels the caller's ctx: Run must return promptly (the hijacked
// exec connection is detached from ctx, so it has to be closed explicitly
// for the demux goroutine to drain) and the in-container process must be
// killed rather than leaked.
func TestDockerProcRunCancellation(t *testing.T) {
	requireDocker(t)
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	name := fmt.Sprintf("test-cancel-%d", time.Now().UnixMilli())
	env, err := dockerrun.NewDockerEnvironment(ctx, resources.NewDockerImage("alpine:3.19.1"), shared.Must(shared.SolvePath("testdata")), name)
	require.NoError(t, err)
	defer func() {
		_ = env.Shutdown(ctx)
	}()

	env.WithPause()
	err = env.Init(ctx)
	require.NoError(t, err)

	proc, err := env.NewProcess("sh", "good/infinite_counter.sh")
	require.NoError(t, err)
	proc.WithOutput(shared.NewSliceWriter())

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	start := time.Now()
	err = proc.Run(runCtx)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, elapsed, 10*time.Second, "Run must return promptly after the deadline")

	require.Eventually(t, func() bool {
		running, err := proc.IsRunning(ctx)
		return err == nil && !running
	}, 10*time.Second, 500*time.Millisecond, "in-container process must be killed on cancellation")
}

func TestFindFreePort(t *testing.T) {
	port, err := base.FindFreePort()
	require.NoError(t, err)
	require.Greater(t, port, 0)

	// The port should still be free right after (no listener)
	require.True(t, base.IsFreePort(port))
}
