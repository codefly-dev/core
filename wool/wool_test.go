package wool_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/codefly-dev/core/resources"
	wool "github.com/codefly-dev/core/wool"
	"github.com/codefly-dev/core/wool/adapters/log"
	"github.com/stretchr/testify/require"
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
	require.NotNil(t, w)

	logger := &testLogger{}
	w.WithLogger(logger)

	err := fmt.Errorf("test error")

	werr := w.Wrap(err)

	require.EqualError(t, werr, "test error")

	log.AsLog(w).Info("hello %s", "world")

	require.Equal(t, 1, len(logger.logs))
	require.Equal(t, "hello world", logger.logs[0].Message)
}

func TestWoolBasics(t *testing.T) {
	ctx := context.Background()
	provider := wool.New(ctx, resources.CLI.AsResource())

	logger := &testLogger{}
	provider.WithLogger(logger)
	defer provider.Done()

	ctx = provider.Inject(ctx)

	w := wool.Get(ctx).In("testBasics", wool.Field("test", "test"))

	require.NotNil(t, w)

	err := fmt.Errorf("test error")
	werr := w.Wrap(err)
	require.EqualError(t, werr, "testBasics: test error")

	// Use the standard logger interface
	log.AsLog(w).Info("hello %s", "world")

	w.Close()

	require.Equal(t, 1, len(logger.logs))
	require.Equal(t, "hello world", logger.logs[0].Message)
}

func TestWoolWithContext(t *testing.T) {
	ctx := context.Background()
	provider := wool.New(ctx, resources.CLI.AsResource())

	logger := &testLogger{}
	provider.WithLogger(logger)
	defer provider.Done()

	ctx = provider.Inject(ctx)

	w := wool.Get(ctx)

	require.NotNil(t, w)

	// Use the standard logger interface
	log.AsLog(w).Info("hello %s", "world")

	w.Close()

	require.Equal(t, 1, len(logger.logs))
	require.Equal(t, "hello world", logger.logs[0].Message)
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
