package base_test

import (
	"context"
	"testing"
	"time"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/wool"

	"github.com/stretchr/testify/assert"

	"github.com/codefly-dev/core/runners/base"
)

func TestLocalEnvironment(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	env, err := base.NewLocalEnvironment(ctx, shared.Must(shared.SolvePath("testdata")))
	assert.NoError(t, err)

	err = env.Init(ctx)
	assert.NoError(t, err)

	// Now, run something in it
	proc, err := env.NewProcess("ls")
	assert.NoError(t, err)
	output := shared.NewSliceWriter()
	proc.WithOutput(output)

	err = proc.Run(ctx)
	assert.NoError(t, err)

	assert.Contains(t, output.Data, "good")
	assert.Contains(t, output.Data, "crashing")

	// re-init should give the same id
	err = env.Init(ctx)
	assert.NoError(t, err)

	// Now, run something in it
	proc, err = env.NewProcess("ls")
	assert.NoError(t, err)
	output = shared.NewSliceWriter()
	proc.WithOutput(output)

	err = proc.Run(ctx)
	assert.NoError(t, err)

	assert.Contains(t, output.Data, "good")
	assert.Contains(t, output.Data, "crashing")

	// Run a finite script
	proc, err = env.NewProcess("sh", "good/finite_counter.sh")
	assert.NoError(t, err)
	output = shared.NewSliceWriter()
	proc.WithOutput(output)

	err = proc.Run(ctx)
	assert.NoError(t, err)
	assert.Contains(t, output.Data, "1")

	// Run an infinite script and stop it after 2 seconds
	proc, err = env.NewProcess("sh", "good/infinite_counter.sh")
	assert.NoError(t, err)
	output = shared.NewSliceWriter()
	proc.WithOutput(output)

	go func() {
		wait := time.NewTimer(time.Second)
		<-wait.C
		err := proc.Stop(ctx)
		assert.NoError(t, err)
	}()

	err = proc.Run(ctx)
	assert.NoError(t, err)

	assert.Contains(t, output.Data, "1")

	proc, err = env.NewProcess("sh", "good/finite_counter.sh")
	assert.NoError(t, err)
	output = shared.NewSliceWriter()
	proc.WithOutput(output)

	err = proc.Run(ctx)
	assert.NoError(t, err)
	assert.Contains(t, output.Data, "1")

	err = env.Shutdown(ctx)
	assert.NoError(t, err)
}
