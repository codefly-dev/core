package runners_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/codefly-dev/core/shared"

	"github.com/stretchr/testify/assert"

	"github.com/codefly-dev/core/runners"
)

func waitSome() {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		time.Sleep(10 * time.Second)
		return
	}
	time.Sleep(2500 * time.Millisecond)
}

func TestCheckBin(t *testing.T) {
	_, err := runners.NewRunner(context.Background(), "echo")
	assert.NoError(t, err)
	_, err = runners.NewRunner(context.Background(), "doesntexist")
	assert.Error(t, err)

}

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
	runner, err := runners.NewRunner(ctx, "echo", "hello")
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
	for i := 0; i < 5; i++ {
		t.Run(fmt.Sprintf("SubTest%d", i), func(t *testing.T) {
			t.Parallel() // This will run the subtest in a separate goroutine.
			testRun(t)
		})
	}
}

func testStart(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	runner, err := runners.NewRunner(ctx, "/bin/sh", "good.sh")
	assert.NoError(t, err)
	runner.WithDir(shared.Must(shared.SolvePath("testdata/good")))
	out := NewSliceWriter()
	runner.WithOut(out)

	err = runner.Start()
	assert.NoError(t, err)

	waitSome()
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
	for i := 0; i < 5; i++ {
		t.Run(fmt.Sprintf("SubTest%d", i), func(t *testing.T) {
			t.Parallel() // This will run the subtest in a separate goroutine.
			testStart(t)
		})
	}
}

func testStartWithStop(t *testing.T) {
	ctx := context.Background()

	runner, err := runners.NewRunner(ctx, "/bin/sh", "good.sh")
	assert.NoError(t, err)
	runner.WithDir(shared.Must(shared.SolvePath("testdata/good")))
	out := NewSliceWriter()
	runner.WithOut(out)

	err = runner.Start()
	assert.NoError(t, err)

	waitSome()
	err = runner.Stop()
	assert.NoError(t, err)
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
	for i := 0; i < 5; i++ {
		t.Run(fmt.Sprintf("SubTest%d", i), func(t *testing.T) {
			t.Parallel() // This will run the subtest in a separate goroutine.
			testStartWithStop(t)
		})
	}
}

func testStartError(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	runner, err := runners.NewRunner(ctx, "go", "run", "main.go")
	assert.NoError(t, err)
	runner.WithDir(shared.Must(shared.SolvePath("testdata/bad")))
	out := NewSliceWriter()
	runner.WithOut(out)

	err = runner.Start()
	assert.NoError(t, err)

	waitSome()
	cancel()

	// Will have a compile error
	assert.Greater(t, len(out.Data()), 1)
	assert.Contains(t, out.Data(), "# command-line-arguments")

	for _, o := range out.Data() {
		assert.NotContains(t, o, "file already closed")
	}
}

func TestStartError(t *testing.T) {
	for i := 0; i < 5; i++ {
		t.Run(fmt.Sprintf("SubTest%d", i), func(t *testing.T) {
			t.Parallel() // This will run the subtest in a separate goroutine.
			testStartError(t)
		})
	}
}

func testStartCrash(t *testing.T) {
	ctx := context.Background()
	runner, err := runners.NewRunner(ctx, "/bin/sh", "crash.sh")
	assert.NoError(t, err)
	runner.WithDir(shared.Must(shared.SolvePath("testdata/crashing")))
	out := NewSliceWriter()
	runner.WithOut(out)

	err = runner.Start()
	assert.NoError(t, err)

	// Wait
	err = runner.Wait()
	assert.Error(t, err)

	for _, o := range out.Data() {
		assert.NotContains(t, o, "file already closed")
	}
}

func TestStartCrashWithStop(t *testing.T) {
	for i := 0; i < 5; i++ {
		t.Run(fmt.Sprintf("SubTest%d", i), func(t *testing.T) {
			t.Parallel() // This will run the subtest in a separate goroutine.
			testStartCrash(t)
		})

	}
}

//
//func TestPortStop(t *testing.T) {
//	ctx := context.Background()
//	runner, err := runners.NewRunner(ctx, "go", "run", "main.go")
//	assert.NoError(t, err)
//	runner.WithDir(shared.Must(shared.SolvePath("testdata/port")))
//	out := NewSliceWriter()
//	runner.WithOut(out)
//
//	err = runner.Start()
//	assert.NoError(t, err)
//
//	waitSome()
//
//	err = runner.Stop()
//	assert.NoError(t, err)
//	// We should have at least a few lines of output
//	expected := []string{"running 0", "running 1", "running 2", "running 3", "running 4"}
//	for _, exp := range expected {
//		assert.Contains(t, out.Data(), exp)
//	}
//	for _, o := range out.Data() {
//		assert.NotContains(t, o, "file already closed")
//	}
//	// Check that the port is free
//	assert.True(t, runners.IsFreePort(33333))
//}
//
//func TestPortContext(t *testing.T) {
//	ctx := context.Background()
//	ctx, cancel := context.WithCancel(ctx)
//
//	runner, err := runners.NewRunner(ctx, "go", "run", "main.go")
//	assert.NoError(t, err)
//	runner.WithDir(shared.Must(shared.SolvePath("testdata/port")))
//	out := NewSliceWriter()
//	runner.WithOut(out)
//
//	err = runner.Start()
//	assert.NoError(t, err)
//
//	waitSome()
//	cancel()
//
//	assert.NoError(t, err)
//	// We should have at least a few lines of output
//	expected := []string{"running 0", "running 1", "running 2", "running 3", "running 4"}
//	for _, exp := range expected {
//		assert.Contains(t, out.Data(), exp)
//	}
//	for _, o := range out.Data() {
//		assert.NotContains(t, o, "file already closed")
//	}
//	// Check that the port is free
//	assert.True(t, runners.IsFreePort(33333))
//}
