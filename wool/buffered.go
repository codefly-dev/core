package wool

import (
	"sync"
	"sync/atomic"
)

// BufferedProcessor wraps a LogProcessor with channel-based async batching.
// Log messages are queued and processed by a background goroutine.
type BufferedProcessor struct {
	inner  LogProcessor
	ch     chan *Log
	done   chan struct{}
	once   sync.Once
	closed atomic.Bool
}

// NewBufferedProcessor creates a buffered wrapper around a LogProcessor.
// bufferSize controls the channel capacity.
func NewBufferedProcessor(inner LogProcessor, bufferSize int) *BufferedProcessor {
	if bufferSize <= 0 {
		bufferSize = 256
	}
	bp := &BufferedProcessor{
		inner: inner,
		ch:    make(chan *Log, bufferSize),
		done:  make(chan struct{}),
	}
	go bp.run()
	return bp
}

func (bp *BufferedProcessor) run() {
	for msg := range bp.ch {
		bp.inner.Process(msg)
	}
	close(bp.done)
}

// Process queues a log message for async processing.
// If the buffer is full, the message is dropped (non-blocking).
// Safe to call concurrently with (and after) Flush: once Flushed, messages
// are dropped rather than sent on the closed channel, which would panic.
func (bp *BufferedProcessor) Process(msg *Log) {
	if bp.closed.Load() {
		return
	}
	select {
	case bp.ch <- msg:
	default:
		// Buffer full -- drop message to avoid blocking the caller.
	}
}

// Flush closes the buffer and waits for all queued messages to be processed.
func (bp *BufferedProcessor) Flush() {
	bp.once.Do(func() {
		// Mark closed before closing the channel so any concurrent Process
		// observes the flag and takes the drop path instead of racing a send
		// onto the about-to-be-closed channel.
		bp.closed.Store(true)
		close(bp.ch)
	})
	<-bp.done
}
