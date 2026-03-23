package lsp

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
	"time"
)

// jsonrpcRequest is a JSON-RPC 2.0 request.
type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      *int        `json:"id,omitempty"` // nil for notifications
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// jsonrpcResponse is a JSON-RPC 2.0 response.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// transport handles JSON-RPC 2.0 over TCP with Content-Length framing.
type transport struct {
	conn   net.Conn
	reader *bufio.Reader

	mu        sync.Mutex
	reqID     int
	pending   map[int]chan json.RawMessage
	readerErr error // set when the reader goroutine exits with an error
}

// newTransport creates a transport from an established TCP connection and
// starts the background response reader.
func newTransport(conn net.Conn) *transport {
	t := &transport{
		conn:    conn,
		reader:  bufio.NewReader(conn),
		pending: make(map[int]chan json.RawMessage),
	}
	go t.readResponses()
	return t
}

// call sends a JSON-RPC request and waits for the response.
func (t *transport) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	t.mu.Lock()
	t.reqID++
	id := t.reqID
	ch := make(chan json.RawMessage, 1)
	t.pending[id] = ch
	t.mu.Unlock()

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	}

	if err := t.send(req); err != nil {
		return nil, err
	}

	select {
	case result, ok := <-ch:
		if !ok {
			// Channel closed by reader goroutine due to error.
			t.mu.Lock()
			err := t.readerErr
			t.mu.Unlock()
			if err != nil {
				return nil, fmt.Errorf("%s: transport reader error: %w", method, err)
			}
			return nil, fmt.Errorf("%s: transport closed", method)
		}
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// notify sends a JSON-RPC notification (no response expected).
func (t *transport) notify(method string, params interface{}) error {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return t.send(req)
}

// send marshals and writes a JSON-RPC message with Content-Length framing.
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

// readResponses reads JSON-RPC messages and dispatches responses to pending callers.
func (t *transport) readResponses() {
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

		var resp jsonrpcResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			continue
		}

		if resp.ID != nil {
			t.mu.Lock()
			if ch, ok := t.pending[*resp.ID]; ok {
				if resp.Error != nil {
					errJSON, _ := json.Marshal(resp.Error)
					ch <- errJSON
				} else {
					ch <- resp.Result
				}
				delete(t.pending, *resp.ID)
			}
			t.mu.Unlock()
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

// waitForConnection retries TCP connection until the LSP server is actually
// ready. A simple TCP dial is NOT enough -- Docker's port proxy accepts
// connections before the LSP server inside the container is listening.
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
			return nil, fmt.Errorf("timeout waiting for LSP server on %s", addr)
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
					// LSP server won't send anything until we do.
					return conn, nil
				}
				// EOF or connection reset = Docker proxy forwarded to a
				// non-ready backend. Retry.
				conn.Close()
				time.Sleep(500 * time.Millisecond)
				continue
			}
			// Got data (unexpected from LSP), but connection works.
			return conn, nil
		}
	}
}
