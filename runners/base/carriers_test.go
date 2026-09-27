package base_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/internal/runnablefixture"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

const carrierChild = "CODEFLY_TEST_CARRIER_CHILD"

// TestCarrierChildProcess is the process the runner starts in
// TestANativeProcessReceivesTwentyFourOperations. It is a worker in
// miniature: it reads its environment the way the SDK does
// (resources.ResolveFileCarriers), then installs every prepared operation
// as a worker does (runnablefixture.Install): verify its package and
// binding, resolve its owner's descriptor set by digest, and find the method
// in it.
func TestCarrierChildProcess(t *testing.T) {
	if os.Getenv(carrierChild) == "" {
		t.Skip("run by TestANativeProcessReceivesTwentyFourOperations")
	}
	total := 0
	for _, entry := range os.Environ() {
		if len(entry)+1 >= resources.MaxEnvironmentStringBytes {
			fmt.Printf("OVERSIZED %s\n", strings.SplitN(entry, "=", 2)[0])
		}
		if strings.HasPrefix(entry, "CODEFLY__") {
			total += len(entry) + 1
		}
		if key, value, _ := strings.Cut(entry, "="); resources.IsFileCarrierKey(key) {
			fmt.Printf("CARRIER_DIR %s\n", filepath.Dir(value))
		}
	}
	fmt.Printf("CODEFLY_BYTES %d\n", total)
	files, err := resources.ResolveFileCarriers(os.Environ())
	if err != nil {
		fmt.Printf("ERROR %v\n", err)
		return
	}
	lookup := func(key string) (string, error) {
		carrier := resources.WorkspaceConfigurationPrefix + "__RUNNABLE_BINDINGS__" + key
		if value := os.Getenv(carrier); value != "" {
			return value, nil
		}
		if value, ok := files[carrier]; ok {
			return value, nil
		}
		return "", fmt.Errorf("no value for %s", key)
	}
	installed := 0
	for _, key := range strings.Split(os.Getenv(carrierChild), ",") {
		value, err := lookup(key)
		if err == nil {
			_, err = runnablefixture.Install(value, lookup)
		}
		if err != nil {
			fmt.Printf("ERROR %s: %v\n", key, err)
			continue
		}
		installed++
	}
	fmt.Printf("INSTALLED %d\n", installed)
}

// A native process is started with a workspace's twenty-four prepared
// operations, as a worker is: the shared descriptor sets arrive by file, no
// environment string reaches Linux's 128 KiB limit, and the process installs
// every operation from what it received. The files are gone once it exits.
func TestANativeProcessReceivesTwentyFourOperations(t *testing.T) {
	ctx := context.Background()
	fixture, err := runnablefixture.Build(24, 2)
	require.NoError(t, err)
	manager := resources.NewEnvironmentVariableManager()
	manager.SetEnvironment(&basev0.Environment{Name: "local"})
	require.NoError(t, manager.AddConfigurations(ctx, fixture.Configuration()))
	envs, err := manager.All()
	require.NoError(t, err)

	env, err := base.NewNativeEnvironment(ctx, shared.Must(shared.SolvePath("testdata")))
	require.NoError(t, err)
	require.NoError(t, env.Init(ctx))
	env.WithEnvironmentVariables(ctx, envs...)
	var keys []string
	for _, operation := range fixture.Operations {
		keys = append(keys, operation.Key)
	}
	proc, err := env.NewProcess(os.Args[0], "-test.run=^TestCarrierChildProcess$", "-test.v")
	require.NoError(t, err)
	proc.WithEnvironmentVariables(ctx, resources.Env(carrierChild, strings.Join(keys, ",")))
	output := shared.NewSliceWriter()
	proc.WithOutput(output)
	require.NoError(t, proc.Run(ctx))

	lines := strings.Join(output.Snapshot(), "\n")
	require.NotContains(t, lines, "OVERSIZED")
	require.NotContains(t, lines, "ERROR")
	require.Contains(t, lines, "INSTALLED 24")
	var codeflyBytes int
	var carrierDir string
	for _, line := range output.Snapshot() {
		if _, err := fmt.Sscanf(line, "CODEFLY_BYTES %d", &codeflyBytes); err == nil {
			continue
		}
		if dir, found := strings.CutPrefix(line, "CARRIER_DIR "); found {
			carrierDir = dir
		}
	}
	require.Positive(t, codeflyBytes)
	require.Less(t, codeflyBytes, 1<<20)
	t.Logf("the process started with %d bytes of Codefly environment", codeflyBytes)
	require.NotEmpty(t, carrierDir, "the descriptor sets arrive by file")
	require.Eventually(t, func() bool {
		_, err := os.Stat(carrierDir)
		return os.IsNotExist(err)
	}, 5*time.Second, 20*time.Millisecond, "the carrier directory is removed once the process exits")
}

// A process whose environment the platform would refuse is not started: the
// runner reports the key, not the kernel's E2BIG.
func TestANativeProcessIsNotStartedWithAnOversizedEnvironment(t *testing.T) {
	ctx := context.Background()
	env, err := base.NewNativeEnvironment(ctx, shared.Must(shared.SolvePath("testdata")))
	require.NoError(t, err)
	require.NoError(t, env.Init(ctx))
	// A raw value is not a workspace configuration value and has no file
	// carrier: it must fit, or the start fails early.
	env.WithEnvironmentVariables(ctx, resources.Env("CODEFLY__RAW", strings.Repeat("v", resources.MaxEnvironmentStringBytes)))
	proc, err := env.NewProcess("true")
	require.NoError(t, err)
	err = proc.Run(ctx)
	require.ErrorIs(t, err, resources.ErrEnvironmentLimit)
	require.Contains(t, err.Error(), "CODEFLY__RAW")
}
