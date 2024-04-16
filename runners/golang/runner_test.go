package golang_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/codefly-dev/core/configurations"

	"github.com/codefly-dev/core/runners/golang"

	"github.com/codefly-dev/core/wool"

	"github.com/stretchr/testify/assert"

	"github.com/codefly-dev/core/shared"
)

func testGo(t *testing.T, ctx context.Context, env *golang.GoRunnerEnvironment) {

	cacheDir, _ := os.MkdirTemp("", "cache")
	env.WithLocalCacheDir(cacheDir)
	goModDir, _ := os.MkdirTemp("", "mod")
	env.WithGoModDir(goModDir)

	defer func() {
		err := env.Shutdown(ctx)
		assert.NoError(t, err)
		err = os.RemoveAll(cacheDir)
		assert.NoError(t, err)
		os.RemoveAll(goModDir)
	}()

	// Init
	err := env.Init(ctx)
	assert.NoError(t, err)

	// Check that the go mod has some modules
	assert.False(t, shared.Must(shared.CheckEmptyDirectory(ctx, goModDir)))

	// Check that the binary is there
	assert.False(t, shared.Must(shared.CheckEmptyDirectory(ctx, cacheDir)))

	assert.False(t, env.UsedCache())

	// Re-init
	err = env.Init(ctx)
	assert.NoError(t, err)
	assert.True(t, env.UsedCache())

	// Run and stop
	proc, err := env.NewProcess()
	assert.NoError(t, err)

	output := shared.NewSliceWriter()
	proc.WithOutput(output)

	go func() {
		wait := time.NewTimer(time.Second)
		<-wait.C
		err := proc.Stop(ctx)
		assert.NoError(t, err)
	}()

	err = proc.Run(ctx)
	assert.NoError(t, err)

	assert.True(t, len(output.Data) > 0, "running")
	for _, line := range output.Data {
		assert.Contains(t, line, "running")
	}

	// Start and stop
	otherProc, err := env.NewProcess()
	assert.NoError(t, err)

	data := shared.NewSliceWriter()
	otherOutput := shared.NewSignalWriter(data)
	otherProc.WithOutput(otherOutput)

	err = otherProc.Start(ctx)
	assert.NoError(t, err)
	fmt.Println("after start")
	timeout := time.NewTimer(5 * time.Second)

	for {
		select {
		case <-otherOutput.Signal():
			fmt.Println("got some data")
			// Data has been written to the output
			assert.True(t, len(data.Data) > 0, "running")
			for _, line := range data.Data {
				assert.Contains(t, line, "running")
				assert.NotContains(t, line, "running\n")
			}
			err = otherProc.Stop(ctx)
			assert.NoError(t, err)
			return
		case <-timeout.C:
			// One second has passed
			t.Fatal("timeout")
		}

	}
}

func TestLocalRunWithMod(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	env, err := golang.NewLocalGoRunner(ctx, shared.MustSolvePath("testdata/mod"))
	assert.NoError(t, err)
	testGo(t, ctx, env)
}

func TestDockerRunWithMod(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	env, err := golang.NewDockerGoRunner(ctx,
		configurations.NewDockerImage("golang:alpine"),
		shared.MustSolvePath("testdata/mod"),
		"test")
	assert.NoError(t, err)

	err = env.Clear(ctx)
	assert.NoError(t, err)

	testGo(t, ctx, env)
}
