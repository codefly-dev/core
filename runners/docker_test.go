package runners_test

import (
	"context"
	"testing"
	"time"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/runners"
	"github.com/stretchr/testify/assert"
)

func TestDockerStart(t *testing.T) {
	ctx := context.Background()
	wool.SetGlobalLogLevel(wool.DEBUG)
	// Run sleeping alpine
	runner, err := runners.NewDocker(ctx)
	assert.NoError(t, err)
	runner.WithMount(shared.Must(shared.SolvePath("testdata")), "/codefly")
	localPort := 35123
	runner.WithPort(runners.DockerPortMapping{
		Host:      localPort,
		Container: 8080,
	})

	out := NewSliceWriter()
	runner.WithOut(out)

	err = runner.Init(ctx, runners.DockerImage{
		Name: "alpine",
		Tag:  "latest",
	})
	assert.NoError(t, err)
	runner.WithCommand("/bin/sh", "/codefly/counter.sh")

	// Start
	err = runner.Start(ctx)
	assert.NoError(t, err)
	time.Sleep(2 * time.Second)

	// Check portMapping is binding
	assert.False(t, runners.IsFreePort(localPort))

	assert.Greater(t, len(out.Data()), 0)
	expected := []string{"1", "2", "3", "4", "5"}
	for _, e := range expected {
		assert.Contains(t, out.Data(), e)
	}
	// Stop
	err = runner.Stop()
	assert.NoError(t, err)
	time.Sleep(2 * time.Second)

	// Port is free
	assert.True(t, runners.IsFreePort(localPort))

}
