package agents

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/codefly-dev/core/wool"
)

var (
	handler *LogHandler
)

// LogHandler processes structured logs from agent processes.
type LogHandler struct {
	processors []wool.LogProcessorWithSource
}

func AddProcessor(processor wool.LogProcessorWithSource) {
	handler.processors = append(handler.processors, processor)
}

func init() {
	handler = &LogHandler{}
}

func GetLogHandler() *LogHandler {
	return handler
}

// ForwardLogs reads structured JSON log lines from a reader (typically
// the stderr of a spawned agent process) and dispatches them to
// registered log processors. Blocks until the reader is closed.
func (h *LogHandler) ForwardLogs(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var msg LogMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			// Not a structured log -- treat as plain text trace
			h.process(wool.System(), &wool.Log{Level: wool.TRACE, Message: line, Header: "agent"})
			continue
		}

		if msg.Log == nil {
			continue
		}

		h.process(msg.Source, msg.Log)
	}
}

func (h *LogHandler) process(identifier *wool.Identifier, log *wool.Log) {
	for _, processor := range h.processors {
		processor.ProcessWithSource(identifier, log)
	}
}

// ChannelProcessor sends logs to a channel for consumption by a TUI or
// other event-driven consumer.
type ChannelProcessor struct {
	Ch chan<- ChannelLog
}

// ChannelLog carries a structured log with its source through a channel.
type ChannelLog struct {
	Source *wool.Identifier
	Log    *wool.Log
}

func (p *ChannelProcessor) Process(log *wool.Log) {
	select {
	case p.Ch <- ChannelLog{Log: log}:
	default:
	}
}

func (p *ChannelProcessor) ProcessWithSource(source *wool.Identifier, log *wool.Log) {
	select {
	case p.Ch <- ChannelLog{Source: source, Log: log}:
	default:
	}
}

// NewChannelProcessor creates a processor that sends logs on ch.
// The channel should be buffered to avoid blocking the log pipeline.
func NewChannelProcessor(ch chan<- ChannelLog) *ChannelProcessor {
	return &ChannelProcessor{Ch: ch}
}
