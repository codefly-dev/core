package manager

import (
	"io"
	"testing"
	"time"
)

// TestAgentConnClose_ClosesLogWriter verifies Close closes the WithLogWriter
// sink so the ForwardLogs goroutine reading the other end of the pipe
// unblocks on EOF instead of leaking for the daemon's lifetime.
func TestAgentConnClose_ClosesLogWriter(t *testing.T) {
	pr, pw := io.Pipe()

	// Stand in for the ForwardLogs goroutine: it blocks on the reader until
	// the writer is closed.
	unblocked := make(chan struct{})
	go func() {
		defer close(unblocked)
		_, _ = io.Copy(io.Discard, pr)
	}()

	// cmd is nil, so Close takes the no-process path but still runs the
	// deferred closeLogWriter.
	c := &AgentConn{logWriter: pw}
	c.Close()

	select {
	case <-unblocked:
	case <-time.After(2 * time.Second):
		t.Fatal("log-forwarding reader did not unblock — Close left the pipe writer open")
	}
}

// TestAgentConnClose_NilLogWriter ensures Close is a no-op safe when no
// WithLogWriter sink was supplied.
func TestAgentConnClose_NilLogWriter(t *testing.T) {
	c := &AgentConn{}
	c.Close() // must not panic
}
