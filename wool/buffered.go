package wool

import "sync"

// BufferedProcessor wraps a LogProcessor with channel-based async batching.
// Log messages are queued and processed by a background goroutine.
type BufferedProcessor struct {
	inner LogProcessor
	ch    chan *Log
	done  chan struct{}
	once  sync.Once
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
func (bp *BufferedProcessor) Process(msg *Log) {
	select {
	case bp.ch <- msg:
	default:
		// Buffer full -- drop message to avoid blocking the caller.
	}
}

// Flush closes the buffer and waits for all queued messages to be processed.
func (bp *BufferedProcessor) Flush() {
	bp.once.Do(func() {
		close(bp.ch)
	})
	<-bp.done
}
