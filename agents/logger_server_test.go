package agents

import (
	"encoding/json"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codefly-dev/core/wool"
)

// countingProcessor records how many logs it received.
type countingProcessor struct {
	n atomic.Int64
}

func (p *countingProcessor) Process(*wool.Log)                             { p.n.Add(1) }
func (p *countingProcessor) ProcessWithSource(*wool.Identifier, *wool.Log) { p.n.Add(1) }

func mustLogLine(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(&LogMessage{
		Log:    &wool.Log{Level: wool.INFO, Message: "hello"},
		Source: wool.System(),
	})
	if err != nil {
		t.Fatalf("marshal log line: %v", err)
	}
	return string(data) + "\n"
}

// TestForwardLogs_ConcurrentAddProcessor exercises the data race: a
// ForwardLogs goroutine ranging over processors while AddProcessor appends.
// Run under -race, an unguarded slice would trip the detector.
func TestForwardLogs_ConcurrentAddProcessor(t *testing.T) {
	h := &LogHandler{}

	pr, pw := io.Pipe()
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		h.ForwardLogs(pr)
	}()

	line := mustLogLine(t)

	// Concurrently register processors while logs stream through.
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			h.add(&countingProcessor{})
		})
	}
	for range 200 {
		if _, err := io.WriteString(pw, line); err != nil {
			t.Errorf("write log line: %v", err)
			break
		}
	}

	wg.Wait()
	// Closing the writer must terminate ForwardLogs.
	if err := pw.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	<-forwarded
}

// TestForwardLogs_DispatchesToProcessors verifies logs reach registered
// processors and that closing the reader ends ForwardLogs.
func TestForwardLogs_DispatchesToProcessors(t *testing.T) {
	h := &LogHandler{}
	proc := &countingProcessor{}
	h.add(proc)

	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ForwardLogs(pr)
	}()

	line := mustLogLine(t)
	const count = 10
	for range count {
		if _, err := io.WriteString(pw, line); err != nil {
			t.Fatalf("write log line: %v", err)
		}
	}
	if err := pw.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	<-done

	if got := proc.n.Load(); got != count {
		t.Fatalf("processor received %d logs, want %d", got, count)
	}
}

func TestForwardLogs_AcceptsLogLinesLargerThanScannerDefault(t *testing.T) {
	h := &LogHandler{}
	proc := &countingProcessor{}
	h.add(proc)

	msg, err := json.Marshal(&LogMessage{
		Log:    &wool.Log{Level: wool.INFO, Message: strings.Repeat("x", 128<<10)},
		Source: wool.System(),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.ForwardLogs(strings.NewReader(string(msg) + "\n"))
	if got := proc.n.Load(); got != 1 {
		t.Fatalf("processor received %d logs, want 1", got)
	}
}

func TestForwardLogs_DrainsAfterOversizedLogLine(t *testing.T) {
	h := &LogHandler{}
	pr, pw := io.Pipe()
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		h.ForwardLogs(pr)
	}()

	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(pw, strings.Repeat("x", (4<<20)+1)+"\n")
		if closeErr := pw.Close(); err == nil {
			err = closeErr
		}
		written <- err
	}()

	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("oversized writer remained unhealthy: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("oversized log line blocked the pipe writer")
	}
	<-forwarded
}
