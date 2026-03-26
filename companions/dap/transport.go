package dap

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// dapMessage is the base for all DAP messages (used for type dispatch).
type dapMessage struct {
	Seq  int    `json:"seq"`
	Type string `json:"type"` // "request", "response", "event"
}

// dapRequest is a DAP request message.
type dapRequest struct {
	Seq       int         `json:"seq"`
	Type      string      `json:"type"` // always "request"
	Command   string      `json:"command"`
	Arguments interface{} `json:"arguments,omitempty"`
}

// dapResponse is a DAP response message.
type dapResponse struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"` // always "response"
	RequestSeq int             `json:"request_seq"`
	Success    bool            `json:"success"`
	Command    string          `json:"command"`
	Message    string          `json:"message,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
}

// dapEvent is a DAP event message.
type dapEvent struct {
	Seq   int             `json:"seq"`
	Type  string          `json:"type"` // always "event"
	Event string          `json:"event"`
	Body  json.RawMessage `json:"body,omitempty"`
}

// transport handles DAP messages over TCP with Content-Length framing.
//
// DAP uses the same Content-Length header framing as LSP, but its own
// message format (not JSON-RPC 2.0). Messages are typed: request,
// response, and event. Responses are matched to requests by sequence
// number. Events are dispatched to registered handlers.
type transport struct {
	conn   net.Conn
	reader *bufio.Reader

	mu        sync.Mutex
	seq       int64
	pending   map[int]chan *dapResponse
	readerErr error // set when the reader goroutine exits with an error

	// Event handlers (protected by eventMu).
	eventMu      sync.RWMutex
	onStopped    func(StoppedEvent)
	onOutput     func(OutputEvent)
	onTerminated func()
}

// newTransport creates a transport from an established TCP connection and
// starts the background message reader.
func newTransport(conn net.Conn) *transport {
	t := &transport{
		conn:    conn,
		reader:  bufio.NewReader(conn),
		pending: make(map[int]chan *dapResponse),
	}
	go t.readMessages()
	return t
}

// request sends a DAP request and waits for the matching response.
func (t *transport) request(ctx context.Context, command string, arguments interface{}) (*dapResponse, error) {
	seq := int(atomic.AddInt64(&t.seq, 1))

	ch := make(chan *dapResponse, 1)
	t.mu.Lock()
	t.pending[seq] = ch
	t.mu.Unlock()

	req := dapRequest{
		Seq:       seq,
		Type:      "request",
		Command:   command,
		Arguments: arguments,
	}

	if err := t.send(req); err != nil {
		t.mu.Lock()
		delete(t.pending, seq)
		t.mu.Unlock()
		return nil, err
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			// Channel closed by reader goroutine due to error.
			t.mu.Lock()
			err := t.readerErr
			t.mu.Unlock()
			if err != nil {
				return nil, fmt.Errorf("%s: transport reader error: %w", command, err)
			}
			return nil, fmt.Errorf("%s: transport closed", command)
		}
		if !resp.Success {
			return nil, fmt.Errorf("%s: %s", command, resp.Message)
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// send marshals and writes a DAP message with Content-Length framing.
func (t *transport) send(msg interface{}) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := io.WriteString(t.conn, header); err != nil {
		return err
	}
	if _, err := t.conn.Write(body); err != nil {
		return err
	}
	return nil
}

// readMessages reads DAP messages and dispatches responses to pending callers
// and events to registered handlers.
func (t *transport) readMessages() {
	for {
		header, err := t.readHeader()
		if err != nil {
			t.setErr(fmt.Errorf("readHeader: %w", err))
			return
		}

		contentLength, ok := header["Content-Length"]
		if !ok {
			continue
		}

		length, err := strconv.Atoi(contentLength)
		if err != nil {
			continue
		}

		body := make([]byte, length)
		if _, err := io.ReadFull(t.reader, body); err != nil {
			t.setErr(fmt.Errorf("readBody: %w", err))
			return
		}

		// Peek at the type field to decide how to dispatch.
		var base dapMessage
		if err := json.Unmarshal(body, &base); err != nil {
			continue
		}

		switch base.Type {
		case "response":
			var resp dapResponse
			if err := json.Unmarshal(body, &resp); err != nil {
				continue
			}
			t.mu.Lock()
			if ch, ok := t.pending[resp.RequestSeq]; ok {
				ch <- &resp
				delete(t.pending, resp.RequestSeq)
			}
			t.mu.Unlock()

		case "event":
			var ev dapEvent
			if err := json.Unmarshal(body, &ev); err != nil {
				continue
			}
			t.dispatchEvent(ev)
		}
	}
}

// dispatchEvent routes DAP events to registered handlers.
func (t *transport) dispatchEvent(ev dapEvent) {
	t.eventMu.RLock()
	defer t.eventMu.RUnlock()

	switch ev.Event {
	case "stopped":
		if t.onStopped != nil {
			var body struct {
				ThreadID int    `json:"threadId"`
				Reason   string `json:"reason"`
			}
			_ = json.Unmarshal(ev.Body, &body)
			t.onStopped(StoppedEvent{ThreadID: body.ThreadID, Reason: body.Reason})
		}
	case "output":
		if t.onOutput != nil {
			var body struct {
				Category string `json:"category"`
				Output   string `json:"output"`
			}
			_ = json.Unmarshal(ev.Body, &body)
			t.onOutput(OutputEvent{Category: body.Category, Output: body.Output})
		}
	case "terminated":
		if t.onTerminated != nil {
			t.onTerminated()
		}
	}
}

func (t *transport) setErr(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.readerErr = err
	// Wake up any pending callers.
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
}

func (t *transport) readHeader() (map[string]string, error) {
	headers := make(map[string]string)
	for {
		line, err := t.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) == 2 {
			headers[parts[0]] = parts[1]
		}
	}
	return headers, nil
}

func (t *transport) close() error {
	return t.conn.Close()
}

// waitForConnection retries TCP connection until the DAP server is actually
// ready. A simple TCP dial is NOT enough -- Docker's port proxy accepts
// connections before the DAP server inside the container is listening.
// We verify by holding the connection open briefly and checking it doesn't
// get reset.
func waitForConnection(ctx context.Context, port int) (net.Conn, error) {
	addr := fmt.Sprintf("localhost:%d", port)
	deadline := time.After(30 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, fmt.Errorf("timeout waiting for DAP server on %s", addr)
		default:
			conn, err := net.DialTimeout("tcp", addr, time.Second)
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			// Verify the connection is actually alive by checking it
			// doesn't get reset immediately. Docker's port proxy accepts
			// connections before the backend is ready and then resets them.
			conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			probe := make([]byte, 1)
			_, err = conn.Read(probe)
			conn.SetReadDeadline(time.Time{}) // clear deadline
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// Timeout on read = no data yet = connection is alive.
					// DAP server won't send anything until we do.
					return conn, nil
				}
				// EOF or connection reset = Docker proxy forwarded to a
				// non-ready backend. Retry.
				conn.Close()
				time.Sleep(500 * time.Millisecond)
				continue
			}
			// Got data (unexpected from DAP), but connection works.
			return conn, nil
		}
	}
}
