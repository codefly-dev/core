package wool_test

import (
	"context"
	"github.com/codefly-dev/core/configurations"
	wool "github.com/codefly-dev/core/wool"
	"github.com/codefly-dev/core/wool/adapters/log"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestWoolBasics(t *testing.T) {
	ctx := context.Background()
	w := wool.New(ctx, wool.NewAgent(configurations.CLI))
	defer w.Done()

	ctx = w.NewContext()

	c := wool.Get(ctx)

	assert.NotNil(t, c)
	assert.Equal(t, "cli", c.Name())

	// Setup testing Log exporter
	var logs []*wool.Log
	wool.SetLogExporter(func(msg *wool.Log) {
		logs = append(logs, msg)
	})

	// Use the standard logger interface
	log.AsLog(c).Info("hello %s", "world")

	c.Close()

	assert.Equal(t, 1, len(logs))
	assert.Equal(t, "hello world", logs[0].Message)
}
