package agents

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"sync"

	"github.com/codefly-dev/core/wool"
)

var (
	handler *LogHandler
)

// LogHandler processes structured logs from agent processes.
//
// mu guards processors: ForwardLogs goroutines (one per spawned agent)
// range over the slice via process() while AddProcessor appends to it,
// so any processor registered after an agent starts streaming is a
// concurrent read/append without this lock.
type LogHandler struct {
	mu         sync.RWMutex
	processors []wool.LogProcessorWithSource
}

func AddProcessor(processor wool.LogProcessorWithSource) {
	handler.add(processor)
}

func (h *LogHandler) add(processor wool.LogProcessorWithSource) {
	h.mu.Lock()
	h.processors = append(h.processors, processor)
	h.mu.Unlock()
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
	// Agent logs can legitimately contain generated source, compiler output, or
	// structured fields larger than Scanner's 64 KiB default token limit.
	// Keep a bounded ceiling, but never abandon an io.Pipe reader on overflow:
	// doing so leaves the writer blocked forever and can wedge the agent process.
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
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
	if scanner.Err() != nil {
		_, _ = io.Copy(io.Discard, r)
	}
}

func (h *LogHandler) process(identifier *wool.Identifier, log *wool.Log) {
	// Respect the global log level — don't forward trace/debug noise to the TUI.
	if log.Level < wool.GlobalLogLevel() {
		return
	}
	h.mu.RLock()
	processors := h.processors
	h.mu.RUnlock()
	for _, processor := range processors {
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
