package base_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/runners/base"
	"github.com/stretchr/testify/require"
)

func TestLocalEnvironment(t *testing.T) {
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

	timeout := time.NewTimer(time.Second)
loop:
	for {
		select {
		case <-output.Signal():
			break loop
		case <-timeout.C:
			// One second has passed
			t.Fatal("timeout")
		}

	}
	time.Sleep(100 * time.Millisecond)
	require.False(t, shared.Must(proc.IsRunning(ctx)))
	require.Contains(t, d.Data, "good")
	require.Contains(t, d.Data, "crashing")

	// re-init should give the same id
	err = env.Init(ctx)
	require.NoError(t, err)

	// Now, run something in it
	proc, err = env.NewProcess("ls")
	require.NoError(t, err)
	d = shared.NewSliceWriter()
	output = shared.NewSignalWriter(d)
	proc.WithOutput(output)

	err = proc.Run(ctx)
	require.NoError(t, err)
	timeout = time.NewTimer(time.Second)
loopAgain:
	for {
		select {
		case <-output.Signal():
			break loopAgain
		case <-timeout.C:
			// One second has passed
			t.Fatal("timeout")
		}

	}
	time.Sleep(100 * time.Millisecond)
	require.False(t, shared.Must(proc.IsRunning(ctx)))
	require.Contains(t, d.Data, "good")
	require.Contains(t, d.Data, "crashing")

	// Run a finite script
	proc, err = env.NewProcess("sh", "good/finite_counter.sh")
	require.NoError(t, err)

	d = shared.NewSliceWriter()
	output = shared.NewSignalWriter(d)
	proc.WithOutput(output)

	err = proc.Run(ctx)
	require.NoError(t, err)
	timeout = time.NewTimer(time.Second)
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
	time.Sleep(100 * time.Millisecond)
	require.False(t, shared.Must(proc.IsRunning(ctx)))
	require.Contains(t, d.Data, "1")

	// Run an infinite script and stop it after 2 seconds
	proc, err = env.NewProcess("sh", "good/infinite_counter.sh")
	require.NoError(t, err)

	d = shared.NewSliceWriter()
	output = shared.NewSignalWriter(d)
	proc.WithOutput(output)

	go func() {
		wait := time.NewTimer(time.Second)
		<-wait.C
		err := proc.Stop(ctx)
		require.NoError(t, err)
	}()

	err = proc.Run(ctx)
	require.NoError(t, err)

	timeout = time.NewTimer(time.Second)

loopLastTime:
	for {
		select {
		case <-output.Signal():
			break loopLastTime
		case <-timeout.C:
			// One second has passed
			t.Fatal("timeout")
		}

	}
	time.Sleep(100 * time.Millisecond)
	require.Contains(t, d.Data, "1")

	proc, err = env.NewProcess("sh", "good/finite_counter.sh")
	require.NoError(t, err)

	d = shared.NewSliceWriter()
	output = shared.NewSignalWriter(d)
	proc.WithOutput(output)

	err = proc.Run(ctx)
	require.NoError(t, err)

	timeout = time.NewTimer(time.Second)
loopReallyLastTime:
	for {
		select {
		case <-output.Signal():
			break loopReallyLastTime
		case <-timeout.C:
			// One second has passed
			t.Fatal("timeout")
		}
	}
	time.Sleep(100 * time.Millisecond)
	require.Contains(t, d.Data, "1")

	err = env.Shutdown(ctx)
	require.NoError(t, err)
}

// TestNativeProc_ConcurrentStdoutStderr_NoBufferRace pins the
// fix for a Linux-CI flake where bytes.Buffer was being written
// concurrently by stdout + stderr forwarders. Local macOS never
// reproduced because its stderr stayed empty (no terminal-emulation
// wrapper to inject ANSI escapes). On Ubuntu GH Actions runners,
// a tty wrapper writes \033[6n on stderr — both forwarders fire,
// bytes.Buffer's unsynced cursor scrambles, output gets lost.
//
// The fix is a lockedWriter that serializes Writes to proc.output
// (see native_runner.go). This test forces the same shape locally
// by running a script that writes BOTH to stdout AND stderr in
// rapid succession. With -race, any unsync access to the user-
// supplied bytes.Buffer surfaces immediately.
func TestNativeProc_ConcurrentStdoutStderr_NoBufferRace(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	env, err := base.NewNativeEnvironment(ctx, shared.Must(shared.SolvePath("testdata")))
	require.NoError(t, err)
	require.NoError(t, env.Init(ctx))

	// `sh -c '...'` runs a tiny script that interleaves stdout and
	// stderr writes. If proc.output isn't a lockedWriter, the two
	// forwarders race on bytes.Buffer's internal cursor. -race
	// detector will catch it; even without -race, the output may
	// be empty / scrambled / partially-lost.
	proc, err := env.NewProcess("sh", "-c",
		`for i in 1 2 3 4 5; do echo "out-$i"; echo "err-$i" >&2; done`)
	require.NoError(t, err)

	var buf bytes.Buffer
	proc.WithOutput(&buf)

	require.NoError(t, proc.Run(ctx))

	got := buf.String()
	// Every line we emitted must land — no scrambling, no losses.
	for _, expected := range []string{"out-1", "out-5", "err-1", "err-5"} {
		require.True(t, strings.Contains(got, expected),
			"output missing %q (race likely): %q", expected, got)
	}
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
