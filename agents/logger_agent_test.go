package agents

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/codefly-dev/core/wool"
)

func TestAgentLoggerSnapshotsMutableFields(t *testing.T) {
	var output bytes.Buffer
	logger := newAgentLogger(
		&wool.Identifier{Kind: "agent", Unique: "test"},
		&output,
		1,
	)

	value := &struct {
		Items []string `json:"items"`
	}{Items: []string{"before"}}
	logger.Process(&wool.Log{
		Level:   wool.INFO,
		Message: "snapshot",
		Fields:  []*wool.LogField{wool.Field("value", value)},
	})
	value.Items[0] = "after"
	logger.Flush()

	line := output.String()
	if !strings.Contains(line, `"items":["before"]`) {
		t.Fatalf("queued log did not preserve its input snapshot: %s", line)
	}
	if strings.Contains(line, "after") {
		t.Fatalf("queued log observed a later mutation: %s", line)
	}
}

func TestAgentLoggerProcessConcurrentWithFlush(t *testing.T) {
	logger := newAgentLogger(
		&wool.Identifier{Kind: "agent", Unique: "test"},
		&bytes.Buffer{},
		64,
	)

	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			for range 1000 {
				logger.Process(&wool.Log{Level: wool.INFO, Message: "message"})
			}
		})
	}
	logger.Flush()
	workers.Wait()
	logger.Flush()
	logger.Process(&wool.Log{Level: wool.INFO, Message: "after close"})
}
