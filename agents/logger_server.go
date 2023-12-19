package agents

import (
	"encoding/json"
	"time"

	"github.com/codefly-dev/core/wool"
	"github.com/hashicorp/go-hclog"
)

var (
	handler *ClientLogHandler
)

// A ClientLogHandler handles logs from the Agents and converts them back to wool.Log
type ClientLogHandler struct {
	receiver   hclog.Logger
	processors []wool.LogProcessorWithSource
}

func AddProcessor(processor wool.LogProcessorWithSource) {
	handler.processors = append(handler.processors, processor)
}

func init() {
	handler = NewLogHandler()
}

func LogHandler() *ClientLogHandler {
	return handler
}

func NewLogHandler() *ClientLogHandler {
	handler := &ClientLogHandler{}
	logger := hclog.New(&hclog.LoggerOptions{
		JSONFormat: true,
		Output:     handler,
		Level:      hclog.Debug,
	})
	handler.receiver = logger
	return handler
}

type HCLogMessageIn struct {
	Level     string    `json:"@level"`
	Timestamp time.Time `json:"@timestamp"`
	Message   string    `json:"@message"`
	Module    string    `json:"@module"`
}

func (h *ClientLogHandler) Write(p []byte) (n int, err error) {
	// We assume that the log is in JSON format
	msg := &HCLogMessageIn{}
	err = json.Unmarshal(p, msg)
	if err != nil {
		return 0, err
	}
	// message is a JSON representation of a wool.Log
	var log HCLogMessageOut
	err = json.Unmarshal([]byte(msg.Message), &log)
	if err != nil {
		return 0, err
	}
	// Drop non-wool logs
	if msg.Message == "" {
		return 0, nil
	}
	err = h.process(log.Log, log.Source)
	if err != nil {
		return 0, err
	}

	return len(p), nil
}

func (h *ClientLogHandler) process(log *wool.Log, identifier *wool.Identifier) error {
	for _, processor := range h.processors {
		processor.ProcessWithSource(log, identifier)
	}
	return nil
}