package runners_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/codefly-dev/core/shared"

	"github.com/stretchr/testify/assert"

	"github.com/codefly-dev/core/runners"
)

type SliceWriter struct {
	sync.RWMutex
	data []string
}

func NewSliceWriter() *SliceWriter {
	return &SliceWriter{
		data: []string{},
	}
}

func (sw *SliceWriter) Write(p []byte) (n int, err error) {
	sw.Lock()
	defer sw.Unlock()
	sw.data = append(sw.data, string(p))
	return len(p), nil
}

func (sw *SliceWriter) Data() []string {
	sw.RLock()
	defer sw.RUnlock()
	return sw.data
}

func (sw *SliceWriter) Close() error {
	return nil
}

func testRun(t *testing.T) {
	ctx := context.Background()
	runner, err := runners.NewRunner(ctx, "test", "echo", "hello")
	assert.NoError(t, err)
	out := NewSliceWriter()
	runner.WithOut(out)
	err = runner.Run()
	assert.NoError(t, err)
	assert.Equal(t, []string{"hello"}, out.Data())
	for _, o := range out.Data() {
		assert.NotContains(t, o, "file already closed")
	}
}

func TestRunSuccess(t *testing.T) {
	for i := 0; i < 10; i++ {
		go testRun(t)
	}
}

func testRunError(t *testing.T) {
	ctx := context.Background()
	runner, err := runners.NewRunner(ctx, "test", "doesntexist")
	assert.NoError(t, err)
	out := NewSliceWriter()
	runner.WithOut(out)
	err = runner.Run()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "executable file not found in $PATH")
	assert.Equal(t, 0, len(out.Data()))
	for _, o := range out.Data() {
		assert.NotContains(t, o, "file already closed")
	}
}

func TestRunError(t *testing.T) {
	for i := 0; i < 10; i++ {
		go testRunError(t)
	}
}

func testStart(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	runner, err := runners.NewRunner(ctx, "test", "go", "run", "main.go")
	assert.NoError(t, err)
	runner.WithDir(shared.Must(shared.SolvePath("testdata/good")))
	out := NewSliceWriter()
	runner.WithOut(out)

	err = runner.Start()
	assert.NoError(t, err)

	time.Sleep(2 * time.Second)
	cancel()
	// We should have at least a few lines of output
	expected := []string{"running 0", "running 1", "running 2", "running 3", "running 4"}
	for _, exp := range expected {
		assert.Contains(t, out.Data(), exp)
	}
	for _, o := range out.Data() {
		assert.NotContains(t, o, "file already closed")
	}
}

func TestStart(t *testing.T) {
	for i := 0; i < 10; i++ {
		go testStart(t)
	}
}

func testStartWithStop(t *testing.T) {
	ctx := context.Background()
	runner, err := runners.NewRunner(ctx, "test", "go", "run", "main.go")
	assert.NoError(t, err)
	runner.WithDir(shared.Must(shared.SolvePath("testdata/good")))
	out := NewSliceWriter()
	runner.WithOut(out)

	err = runner.Start()
	assert.NoError(t, err)

	time.Sleep(2 * time.Second)
	runner.Stop()
	// We should have at least a few lines of output
	expected := []string{"running 0", "running 1", "running 2", "running 3", "running 4"}
	for _, exp := range expected {
		assert.Contains(t, out.Data(), exp)
	}
	for _, o := range out.Data() {
		assert.NotContains(t, o, "file already closed")
	}
}

func TestStartWithStop(t *testing.T) {
	for i := 0; i < 10; i++ {
		go testStartWithStop(t)
	}
}

func testStartError(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	runner, err := runners.NewRunner(ctx, "test", "go", "run", "main.go")
	assert.NoError(t, err)
	runner.WithDir(shared.Must(shared.SolvePath("testdata/bad")))
	out := NewSliceWriter()
	runner.WithOut(out)

	err = runner.Start()
	assert.NoError(t, err)

	time.Sleep(2 * time.Second)
	cancel()

	// Will have a compile error
	assert.Greater(t, len(out.Data()), 1)
	assert.Contains(t, out.Data(), "# command-line-arguments")

	for _, o := range out.Data() {
		assert.NotContains(t, o, "file already closed")
	}
}

func TestStartError(t *testing.T) {
	for i := 0; i < 3; i++ {
		go testStartError(t)
	}
}

func testStartCrash(t *testing.T) {
	ctx := context.Background()
	runner, err := runners.NewRunner(ctx, "test", "go", "run", "main.go")
	assert.NoError(t, err)
	runner.WithDir(shared.Must(shared.SolvePath("testdata/crashing")))
	out := NewSliceWriter()
	runner.WithOut(out)

	err = runner.Start()
	assert.NoError(t, err)

	// Wait
	err = runner.Wait()
	assert.Error(t, err)

	assert.Greater(t, len(out.Data()), 1)
	assert.Contains(t, out.Data()[0], "panic: oops")

	for _, o := range out.Data() {
		assert.NotContains(t, o, "file already closed")
	}
}

func TestStartCrashWithStop(t *testing.T) {
	for i := 0; i < 10; i++ {
		go testStartCrash(t)
	}
}
