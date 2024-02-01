package golang_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/codefly-dev/core/shared"

	golang "github.com/codefly-dev/core/agents/helpers/go"
)

type SliceWriter struct {
	Data []string
}

func NewSliceWriter() *SliceWriter {
	return &SliceWriter{
		Data: []string{},
	}
}

func (sw *SliceWriter) Write(p []byte) (n int, err error) {
	sw.Data = append(sw.Data, string(p))
	return len(p), nil
}

func (sw *SliceWriter) Close() error {
	return nil
}

func TestBuildNormal(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	runner, err := golang.NewRunner(ctx, shared.Must(shared.SolvePath("testdata/good")))
	defer func() {
		err := os.RemoveAll(runner.CacheDir())
		assert.NoError(t, err)
	}()

	assert.NoError(t, err)
	err = runner.Init(ctx)
	assert.NoError(t, err)
	assert.False(t, runner.UsedCache())
	// Re-build
	err = runner.Init(ctx)
	assert.NoError(t, err)
	assert.True(t, runner.UsedCache())
	// Start

	// Setup the out
	out := NewSliceWriter()
	runner.WithOut(out)
	err = runner.Start(ctx)
	time.Sleep(2 * time.Second)
	cancel()

}

func TestBuildDebug(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	runner, err := golang.NewRunner(ctx, shared.Must(shared.SolvePath("testdata/good")))
	defer func() {
		err := os.RemoveAll(runner.CacheDir())
		assert.NoError(t, err)
	}()
	runner.WithDebug(true)

	assert.NoError(t, err)
	err = runner.Init(ctx)
	assert.NoError(t, err)
	assert.False(t, runner.UsedCache())
	// Re-build
	err = runner.Init(ctx)
	assert.NoError(t, err)
	assert.True(t, runner.UsedCache())
	// Start

	// Setup the out
	out := NewSliceWriter()
	runner.WithOut(out)
	err = runner.Start(ctx)
	time.Sleep(2 * time.Second)
	cancel()

}
