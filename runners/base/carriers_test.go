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
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

const carrierChild = "CODEFLY_TEST_CARRIER_CHILD"

// carriedValues is a workspace configuration group whose values are larger
// than a process environment can carry, beside small ones that stay inline.
// The content of each large value follows from its key, so the process started
// with it can say whether what it read is what was delivered.
func carriedValues() (*basev0.Configuration, []string) {
	info := &basev0.ConfigurationInformation{Name: "catalog"}
	var keys []string
	for i := 0; i < 4; i++ {
		key := fmt.Sprintf("BUNDLE_%02d", i)
		keys = append(keys, key)
		info.ConfigurationValues = append(info.ConfigurationValues,
			&basev0.ConfigurationValue{Key: key, Value: carriedValue(key)},
			&basev0.ConfigurationValue{Key: fmt.Sprintf("MODE_%02d", i), Value: "fast"},
		)
	}
	return &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{info}}, keys
}

func carriedValue(key string) string {
	return strings.Repeat(key, resources.MaxEnvironmentStringBytes/len(key)+1)
}

// TestCarrierChildProcess is the process the runner starts in
// TestANativeProcessReceivesValuesTooLargeForItsEnvironment. It reads its
// environment the way an SDK does (resources.ResolveFileCarriers) and reports
// whether each value it was given arrived whole.
func TestCarrierChildProcess(t *testing.T) {
	if os.Getenv(carrierChild) == "" {
		t.Skip("run by TestANativeProcessReceivesValuesTooLargeForItsEnvironment")
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
	read := 0
	for _, key := range strings.Split(os.Getenv(carrierChild), ",") {
		carrier := resources.WorkspaceConfigurationPrefix + "__CATALOG__" + key
		value, found := os.LookupEnv(carrier)
		if !found {
			value, found = files[carrier]
		}
		switch {
		case !found:
			fmt.Printf("ERROR %s: no value\n", key)
		case value != carriedValue(key):
			fmt.Printf("ERROR %s: %d bytes are not the %d delivered\n", key, len(value), len(carriedValue(key)))
		default:
			read++
		}
	}
	fmt.Printf("READ %d\n", read)
}

// A native process is started with configuration values larger than its
// environment can carry: they arrive by file, no environment string reaches
// Linux's 128 KiB limit, and the process reads every one of them whole through
// the same resolution an SDK uses. The files are gone once it exits.
func TestANativeProcessReceivesValuesTooLargeForItsEnvironment(t *testing.T) {
	ctx := context.Background()
	configuration, keys := carriedValues()
	manager := resources.NewEnvironmentVariableManager()
	manager.SetEnvironment(&basev0.Environment{Name: "local"})
	require.NoError(t, manager.AddConfigurations(ctx, configuration))
	envs, err := manager.All()
	require.NoError(t, err)

	env, err := base.NewNativeEnvironment(ctx, shared.Must(shared.SolvePath("testdata")))
	require.NoError(t, err)
	require.NoError(t, env.Init(ctx))
	env.WithEnvironmentVariables(ctx, envs...)
	proc, err := env.NewProcess(os.Args[0], "-test.run=^TestCarrierChildProcess$", "-test.v")
	require.NoError(t, err)
	proc.WithEnvironmentVariables(ctx, resources.Env(carrierChild, strings.Join(keys, ",")))
	output := shared.NewSliceWriter()
	proc.WithOutput(output)
	require.NoError(t, proc.Run(ctx))

	lines := strings.Join(output.Snapshot(), "\n")
	require.NotContains(t, lines, "OVERSIZED")
	require.NotContains(t, lines, "ERROR")
	require.Contains(t, lines, fmt.Sprintf("READ %d", len(keys)))
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
	require.NotEmpty(t, carrierDir, "the large values arrive by file")
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
