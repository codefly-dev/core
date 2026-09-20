package manager

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestCopyFilePublishesCompleteExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "installed")
	require.NoError(t, os.WriteFile(target, []byte("old executable"), 0o755))
	active, err := os.Open(target)
	require.NoError(t, err)
	defer active.Close()
	source := filepath.Join(t.TempDir(), "new")
	require.NoError(t, os.WriteFile(source, []byte("new executable"), 0o755))
	require.NoError(t, CopyFile(source, target))
	previous, err := io.ReadAll(active)
	require.NoError(t, err)
	require.Equal(t, "old executable", string(previous))
	current, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "new executable", string(current))
	info, err := os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestCopyFileFailurePreservesInstall(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "installed")
	require.NoError(t, os.WriteFile(target, []byte("active"), 0o755))
	for _, source := range []string{filepath.Join(dir, "missing"), t.TempDir()} {
		require.Error(t, CopyFile(source, target))
		content, err := os.ReadFile(target)
		require.NoError(t, err)
		require.Equal(t, "active", string(content))
	}
	source := filepath.Join(t.TempDir(), "new")
	require.NoError(t, os.WriteFile(source, []byte("replacement"), 0o755))
	require.Error(t, CopyFile(source, dir))
	entries, err := os.ReadDir(filepath.Dir(dir))
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), ".agent-install-"))
	}
}

func TestConcurrentInstallReadersSeeWholeFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "installed")
	payloads := []string{strings.Repeat("a", 1<<20), strings.Repeat("b", 1<<20)}
	require.NoError(t, os.WriteFile(target, []byte(payloads[0]), 0o755))
	var writers sync.WaitGroup
	done := make(chan struct{})
	for index, payload := range payloads {
		source := filepath.Join(t.TempDir(), "source")
		require.NoError(t, os.WriteFile(source, []byte(payload), 0o755))
		writers.Add(1)
		go func() {
			defer writers.Done()
			for range 20 {
				if err := CopyFile(source, target); err != nil {
					t.Errorf("writer %d: %v", index, err)
					return
				}
			}
		}()
	}
	go func() { writers.Wait(); close(done) }()
	defer writers.Wait()
	for {
		content, err := os.ReadFile(target)
		require.NoError(t, err)
		require.True(t, string(content) == payloads[0] || string(content) == payloads[1], "reader observed a partial or mixed executable")
		select {
		case <-done:
			return
		default:
		}
	}
}

func TestDownloadRejectsUnsafeIdentityBeforeNetwork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, registration := range resources.AgentKindRegistry() {
		a := &resources.Agent{Kind: registration.Resource, Publisher: "example.test", Name: "../../../outside", Version: "1.0.0"}
		_, err := DownloadURL(a)
		require.ErrorContains(t, err, "invalid agent name")
		if registration.AutoDownload {
			err = Download(ctx, a)
			require.ErrorContains(t, err, "invalid agent name")
			require.NotErrorIs(t, err, context.Canceled)
		}
	}
}

func TestAmbiguousCacheIdentityCannotLoadInstalledBinary(t *testing.T) {
	ctx := t.Context()
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	installed, err := resources.ParseAgent(ctx, resources.ServiceAgent, "example.test/widget__1:2")
	require.NoError(t, err)
	location, err := installed.Path(ctx)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(location), 0o750))
	marker := filepath.Join(t.TempDir(), "executed")
	require.NoError(t, os.WriteFile(location, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o755))
	other := &resources.Agent{Kind: resources.ServiceAgent, Publisher: "example.test", Name: "widget", Version: "1__2"}
	downloaded, err := Downloaded(ctx, other)
	require.ErrorContains(t, err, "cache separator")
	require.False(t, downloaded)
	conn, err := Load(ctx, other, WithoutSandbox(), WithoutPrincipal())
	require.Nil(t, conn)
	require.ErrorContains(t, err, "cache separator")
	require.NoFileExists(t, marker)
	_, err = DownloadURL(other)
	require.ErrorContains(t, err, "cache separator")
}

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

	// The fetch runs off the test goroutine: without the context on the
	// request it never returns at all, and this must report that as a failure
	// rather than hang the package until the suite-wide timeout.
	type result struct {
		resp *http.Response
		err  error
	}
	returned := make(chan result, 1)
	go func() {
		resp, err := fetchRelease(ctx, server.URL)
		returned <- result{resp, err}
	}()

	select {
	case got := <-returned:
		if got.err == nil {
			got.resp.Body.Close()
			t.Fatal("a server that never answers must not return a response")
		}
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("got %v, want the caller's deadline to end the fetch", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fetch outlived its context deadline — the context did not reach the request")
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
	// Upper bounds, not just non-zero: a timeout long enough to outlive the
	// user's patience bounds nothing, and would pass a >0 assertion.
	if d := transport.ResponseHeaderTimeout; d == 0 || d > time.Minute {
		t.Fatalf("ResponseHeaderTimeout is %v — a host that accepts and never answers must fail within a minute", d)
	}
	if d := transport.TLSHandshakeTimeout; d == 0 || d > time.Minute {
		t.Fatalf("TLSHandshakeTimeout is %v — a stalled handshake must fail within a minute", d)
	}
	// The idle pool is the one thing http.Get got right by inheriting
	// DefaultTransport: an unset IdleConnTimeout pins a connection to the
	// release CDN for the life of the process.
	if transport.IdleConnTimeout == 0 {
		t.Fatal("IdleConnTimeout is unset — idle connections never expire")
	}
}

// A download killed by the caller's context must stay distinguishable from an
// agent that genuinely does not exist: Load maps both onto
// ErrAgentBinaryNotFound, and a caller deciding whether to retry — or whether
// to stay quiet because the user pressed Ctrl-C — can only tell them apart if
// the cause survives the wrap.
func TestLoadKeepsTheCancellationCauseBehindErrAgentBinaryNotFound(t *testing.T) {
	t.Setenv("AGENT_NIX_FLAKE", "")
	t.Setenv("AGENT_REGISTRY", "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Load(ctx, &resources.Agent{
		Kind:      resources.ServiceAgent,
		Publisher: "codefly.dev",
		Name:      "definitely-not-a-real-agent-xyz",
		Version:   "9.9.9",
	}, WithoutSandbox(), WithoutPrincipal())
	if err == nil {
		t.Fatal("expected the cancelled download to fail the load")
	}
	if !errors.Is(err, ErrAgentBinaryNotFound) {
		t.Errorf("callers still switch on ErrAgentBinaryNotFound: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v — the cancellation cause was flattened out of the chain", err)
	}
}

// errReader fails partway through, the shape a transfer aborted by the
// caller's context presents to the copy.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// The archive is reopened by path for extraction, so the write handle has to
// be closed by the time writeArchive returns. It was leaked on every call —
// success and failure alike — which a second Close proves by reporting the
// handle was still open.
func TestWriteArchiveClosesItsHandle(t *testing.T) {
	payload := "agent archive bytes"
	path := filepath.Join(t.TempDir(), "agent.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := writeArchive(f, strings.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("second Close returned %v — writeArchive left the descriptor open", err)
	}

	// The close must not cost bytes: extraction reads this path afterwards.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatalf("archive holds %q, want %q", got, payload)
	}
}

func TestWriteArchiveClosesItsHandleWhenTheTransferFails(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "agent.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}

	err = writeArchive(f, errReader{err: context.Canceled}, 1024)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the transfer failure to reach the caller", err)
	}
	if err := f.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("second Close returned %v — a failed transfer leaked the descriptor", err)
	}
}
