package golang_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/golang"

	"github.com/codefly-dev/core/wool"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/shared"
)

func testGo(t *testing.T, ctx context.Context, env *golang.GoRunnerEnvironment, withModule bool) {

	cacheDir, _ := os.MkdirTemp("", "cache")
	env.WithLocalCacheDir(cacheDir)
	goModDir, _ := os.MkdirTemp("", "mod")
	env.WithGoModDir(goModDir)

	defer func() {
		err := env.Shutdown(ctx)
		require.NoError(t, err)
		err = os.RemoveAll(cacheDir)
		require.NoError(t, err)
	}()

	// Init
	err := env.Init(ctx)
	require.NoError(t, err)

	// Check that the go mod has some modules
	if withModule {
		require.False(t, shared.Must(shared.CheckEmptyDirectory(ctx, goModDir)))
	}

	// Check that the binary is there
	require.False(t, shared.Must(shared.CheckEmptyDirectory(ctx, cacheDir)))

	require.False(t, env.UsedCache())

	// Re-init
	err = env.Init(ctx)
	require.NoError(t, err)
	require.True(t, env.UsedCache())

	// Run and stop
	proc, err := env.Runner()
	require.NoError(t, err)

	data := shared.NewSliceWriter()
	output := shared.NewSignalWriter(data)
	proc.WithOutput(output)

	err = proc.Start(ctx)
	require.NoError(t, err)

	time.Sleep(1 * time.Second)
	require.True(t, shared.Must(proc.IsRunning(ctx)))

	err = proc.Stop(ctx)
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	testOutput(t, data)

	require.False(t, shared.Must(proc.IsRunning(ctx)))
}

func testOutput(t *testing.T, data *shared.SliceWriter) {
	// Data has been written to the output
	require.True(t, len(data.Data) > 1)
	for _, line := range data.Data[:len(data.Data)-2] {
		require.Contains(t, line, "running")
		require.NotContains(t, line, "running\n")
	}
	require.Contains(t, data.Data[len(data.Data)-1], "signal received")
}

func TestNativeRunWithMod(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	env, err := golang.NewNativeGoRunner(ctx, shared.MustSolvePath("testdata"), "mod")
	require.NoError(t, err)
	testGo(t, ctx, env, true)
}

func TestDockerRunWithMod(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	name := fmt.Sprintf("test-mod-%d", time.Now().UnixMilli())
	env, err := golang.NewDockerGoRunner(ctx,
		resources.NewDockerImage("golang:1.22.2-alpine"),
		shared.MustSolvePath("testdata"), "mod",
		name)
	require.NoError(t, err)

	testGo(t, ctx, env, true)

	err = env.Shutdown(ctx)
	require.NoError(t, err)

}

func TestDockerRunNoMod(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	name := fmt.Sprintf("test-no-mod-%d", time.Now().UnixMilli())
	ctx := context.Background()
	env, err := golang.NewDockerGoRunner(ctx,
		resources.NewDockerImage("golang:1.22.2-alpine"),
		shared.MustSolvePath("testdata"), "no_mod",
		name)
	require.NoError(t, err)

	testGo(t, ctx, env, false)

	err = env.Shutdown(ctx)
	require.NoError(t, err)
}
