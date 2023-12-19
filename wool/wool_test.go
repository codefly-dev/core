package wool_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/codefly-dev/core/configurations"
	wool "github.com/codefly-dev/core/wool"
	"github.com/codefly-dev/core/wool/adapters/log"
	"github.com/stretchr/testify/assert"
)

type testLogger struct {
	logs []*wool.Log
}

func (t *testLogger) Process(msg *wool.Log) {
	t.logs = append(t.logs, msg)
}

func TestDefault(t *testing.T) {
	ctx := context.Background()

	w := wool.Get(ctx)
	assert.NotNil(t, w)

	fmt.Println(w.File())

	logger := &testLogger{}
	w.WithLogger(logger)

	err := fmt.Errorf("test error")

	werr := w.Wrap(err)

	assert.EqualError(t, werr, "test error")

	log.AsLog(w).Info("hello %s", "world")

	assert.Equal(t, 1, len(logger.logs))
	assert.Equal(t, "hello world", logger.logs[0].Message)
}

func TestWoolBasics(t *testing.T) {
	ctx := context.Background()
	provider := wool.New(ctx, configurations.CLI.AsResource())

	logger := &testLogger{}
	provider.WithLogger(logger)
	defer provider.Done()

	ctx = provider.WithContext(ctx)

	w := wool.Get(ctx).In("testBasics", wool.Field("test", "test"))

	assert.NotNil(t, w)

	err := fmt.Errorf("test error")
	werr := w.Wrap(err)
	assert.EqualError(t, werr, "testBasics: test error")

	// Use the standard logger interface
	log.AsLog(w).Info("hello %s", "world")

	w.Close()

	assert.Equal(t, 1, len(logger.logs))
	assert.Equal(t, "hello world", logger.logs[0].Message)
}

func TestWoolWithContext(t *testing.T) {
	ctx := context.Background()
	provider := wool.New(ctx, configurations.CLI.AsResource())

	logger := &testLogger{}
	provider.WithLogger(logger)
	defer provider.Done()

	ctx = provider.WithContext(ctx)

	w := wool.Get(ctx)

	assert.NotNil(t, w)

	// Use the standard logger interface
	log.AsLog(w).Info("hello %s", "world")

	w.Close()

	assert.Equal(t, 1, len(logger.logs))
	assert.Equal(t, "hello world", logger.logs[0].Message)
}

//
//func one(ctx context.Wool, t *testing.T) {
//	c := provider.GetWith(ctx).In("one",
//		provider.InfoField("where", "one"),
//		provider.DebugField("debug", "one"),
//		provider.ErrorField("error", "one"))
//}
//
//func TestStack(t *testing.T) {
//	ctx := context.Background()
//	w := provider.New(ctx, configurations.CLI.AsResource())
//
//	logger := &testLogger{}
//	w.WithLogger(logger)
//	defer w.Done()
//
//	ctx = w.NewContext()
//
//}
