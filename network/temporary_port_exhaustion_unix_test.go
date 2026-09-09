//go:build unix

package network_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/codefly-dev/core/network"

	"github.com/stretchr/testify/require"
)

// descriptorExhaustionChildEnv marks the re-executed child of
// TestAllocateTemporaryPortUnderDescriptorExhaustion. Lowering RLIMIT_NOFILE
// and exhausting descriptors is process-wide, so it must never happen in the
// parent `go test` process.
const descriptorExhaustionChildEnv = "CODEFLY_TEST_PORT_FD_EXHAUSTION_CHILD"

// loweredDescriptorLimit is high enough for the Go runtime and the test binary
// already running, low enough that exhausting the table takes milliseconds.
const loweredDescriptorLimit = 128

func TestAllocateTemporaryPortUnderDescriptorExhaustion(t *testing.T) {
	if os.Getenv(descriptorExhaustionChildEnv) != "1" {
		runDescriptorExhaustionChild(t)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m, err := network.NewRuntimeManager(ctx, nil)
	require.NoError(t, err)

	var limit syscall.Rlimit
	require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit))
	if limit.Cur > loweredDescriptorLimit {
		lowered := limit
		lowered.Cur = loweredDescriptorLimit
		require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lowered))
	}

	var held []*os.File
	defer func() {
		for _, f := range held {
			_ = f.Close()
		}
	}()
	for {
		f, openErr := os.Open(os.DevNull)
		if openErr != nil {
			break
		}
		held = append(held, f)
	}
	require.NotEmpty(t, held, "expected to consume descriptors before exhaustion")

	start := time.Now()
	port, err := m.AllocateTemporaryPort(ctx)
	elapsed := time.Since(start)

	require.Error(t, err, "exhausted descriptors must surface as an allocation error")
	require.Zero(t, port, "a failed allocation must not report a port")
	require.Less(t, elapsed, 2*time.Second,
		"allocation must fail within its budget, not spin; took %s", elapsed)
	// A full descriptor table is a host that may recover, not one that is
	// misconfigured — callers branch on that distinction.
	require.ErrorIs(t, err, network.ErrTemporaryPortUnavailable)
	require.NotErrorIs(t, err, network.ErrTemporaryPortUnsupported)
	// The underlying cause must survive every layer of wrapping, or the
	// operator is left guessing why allocation failed.
	require.ErrorIs(t, err, syscall.EMFILE)
}

func runDescriptorExhaustionChild(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0],
		"-test.run=^TestAllocateTemporaryPortUnderDescriptorExhaustion$",
		"-test.timeout=45s",
		"-test.v",
	)
	cmd.Env = append(os.Environ(), descriptorExhaustionChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "descriptor-exhaustion child failed:\n%s", out)
	require.True(t, strings.Contains(string(out), "PASS"),
		"descriptor-exhaustion child did not pass:\n%s", out)
}
