package manager

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
)

// stalledServer accepts the connection and then holds the handler until the
// test ends or the client goes away — the blackholing-proxy shape that used to
// hang Download forever.
func stalledServer(t *testing.T, beforeStall func(http.ResponseWriter)) *httptest.Server {
	t.Helper()
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if beforeStall != nil {
			beforeStall(w)
		}
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(done) })
	return server
}

func TestFetchReleaseAbortsWhenContextIsCancelledBeforeHeaders(t *testing.T) {
	server := stalledServer(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	resp, err := fetchRelease(ctx, server.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("a server that never answers must not return a response")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want the caller's deadline to end the fetch", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("fetch took %s — the context did not reach the request", elapsed)
	}
}

func TestFetchReleaseAbortsWhenContextIsCancelledMidTransfer(t *testing.T) {
	server := stalledServer(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Length", "1024")
		_, _ = w.Write([]byte("a"))
		w.(http.Flusher).Flush()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp, err := fetchRelease(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	first := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, first); err != nil {
		t.Fatalf("expected the transfer to start: %v", err)
	}

	cancel()
	failed := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, resp.Body)
		failed <- err
	}()
	select {
	case err := <-failed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want the cancelled context to end the body read", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("body read survived cancellation — a stalled transfer is still unstoppable")
	}
}

func TestDownloadHonoursACancelledContext(t *testing.T) {
	agent := &resources.Agent{Kind: resources.ServiceAgent, Publisher: "example.com", Name: "widget", Version: "1.2.3"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Download(ctx, agent)
	if err == nil {
		t.Fatal("Download must fail on a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled to travel from the caller into the transfer", err)
	}
}

// The client bounds the stall cases but must never cap a legitimately slow
// transfer: a client-wide Timeout would kill a large asset on a slow link,
// which is why the caller's context — not a deadline — is what ends it early.
func TestAgentDownloadClientBoundsStallsWithoutCappingTheTransfer(t *testing.T) {
	if agentDownloadClient.Timeout != 0 {
		t.Fatalf("client Timeout is %s — it caps the whole transfer, not just the stall", agentDownloadClient.Timeout)
	}
	transport, ok := agentDownloadClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected transport %T", agentDownloadClient.Transport)
	}
	if transport.ResponseHeaderTimeout == 0 {
		t.Fatal("a host that accepts the connection and never answers would hang forever")
	}
	if transport.TLSHandshakeTimeout == 0 {
		t.Fatal("a stalled TLS handshake would hang forever")
	}
}
