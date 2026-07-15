package services

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codefly-dev/core/agents/helpers/code"
	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

// countWatcherGoroutines counts the live goroutines belonging to a watcher:
// the fsnotify Watcher.Start loop and the SetupWatcher debounce closure. It
// matches on stack frames rather than a global NumGoroutine baseline so it is
// immune to unrelated background goroutines (e.g. wool telemetry).
func countWatcherGoroutines() int {
	// Grow the buffer until the dump fits: runtime.Stack silently truncates to
	// the buffer, and a truncated dump could under-count into a false pass.
	buf := make([]byte, 1<<20)
	for {
		if n := runtime.Stack(buf, true); n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	dump := string(buf)
	n := 0
	for g := range strings.SplitSeq(dump, "\n\n") {
		if strings.Contains(g, "(*Watcher).Start(") || strings.Contains(g, "SetupWatcher.func") {
			n++
		}
	}
	return n
}

func waitWatcherGoroutines(t *testing.T, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got := countWatcherGoroutines()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("watcher goroutines = %d, want %d after %s", got, want, timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSetupWatcher_OutlivesRequestAndStopsExplicitly locks the watcher
// lifetime: SetupWatcher is normally called from Init, whose RPC context is
// cancelled immediately after the response. Runtime state must outlive that
// request and terminate only when StopWatcher is called.
func TestSetupWatcher_OutlivesRequestAndStopsExplicitly(t *testing.T) {
	base := &Base{
		Wool:     wool.Get(context.Background()),
		Location: t.TempDir(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	conf := NewWatchConfiguration(builders.NewDependencies("test"))
	require.NoError(t, base.SetupWatcher(ctx, conf, func(code.Change) error { return nil }))

	waitWatcherGoroutines(t, 2, time.Second) // Start loop + debounce goroutine

	cancel()
	time.Sleep(100 * time.Millisecond)
	waitWatcherGoroutines(t, 2, time.Second)

	base.StopWatcher()
	waitWatcherGoroutines(t, 0, 3*time.Second)
}

// TestSetupWatcher_DrainsInFlightEventOnStop exercises teardown while the
// handler is running and a fresh event is mid-send: Start blocks sending to
// s.Events because the debounce goroutine is busy in the handler. Cancellation
// must still unwind cleanly — Start's in-flight send has to be drained (not
// stranded) so both goroutines exit.
func TestSetupWatcher_DrainsInFlightEventOnStop(t *testing.T) {
	dir := t.TempDir()
	codeDir := filepath.Join(dir, "code")
	require.NoError(t, os.MkdirAll(codeDir, 0o755))
	target := filepath.Join(codeDir, "main.go")
	require.NoError(t, os.WriteFile(target, []byte("package main\n"), 0o644))

	base := &Base{
		Wool:     wool.Get(context.Background()),
		Location: dir,
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	// Only the first handler call blocks. After the drain, the debounce
	// goroutine can invoke the handler again; that later call must return
	// immediately or teardown would deadlock.
	handler := func(code.Change) error {
		once.Do(func() {
			close(entered)
			<-release
		})
		return nil
	}

	ctx := context.Background()
	conf := NewWatchConfiguration(builders.NewDependencies("test",
		builders.NewDependency("code").WithPathSelect(shared.NewSelect("*.go"))))
	require.NoError(t, base.SetupWatcher(ctx, conf, handler))
	time.Sleep(150 * time.Millisecond) // let fsnotify register the dir watches

	// First save → after the debounce window the handler fires and blocks,
	// leaving the debounce goroutine unable to receive.
	save(t, target, "package main\n// one\n")

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler was never invoked")
	}

	// Second save while the handler is blocked → Start parks on the send to
	// s.Events. Then stop mid-handler and release.
	save(t, target, "package main\n// two\n")
	time.Sleep(150 * time.Millisecond) // let the second event reach Start's send
	base.StopWatcher()
	close(release)

	waitWatcherGoroutines(t, 0, 3*time.Second)
}

// save performs an atomic-rename save (write temp → rename over original),
// which is how most editors save and what the directory watcher observes.
func save(t *testing.T, target, content string) {
	t.Helper()
	tmp := target + ".tmp"
	require.NoError(t, os.WriteFile(tmp, []byte(content), 0o644))
	require.NoError(t, os.Rename(tmp, target))
}
