package services

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/codefly-dev/core/agents/helpers/code"
	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/wool"
)

// TestSetupWatcher_DebounceGoroutineExitsOnCancel is the regression for the
// debounce goroutine leak: on context cancellation the fsnotify watcher stops,
// but the debounce goroutine used to block forever reading s.Events (which is
// never closed). Both goroutines must exit when ctx is cancelled.
func TestSetupWatcher_DebounceGoroutineExitsOnCancel(t *testing.T) {
	base := &Base{
		Wool:     wool.Get(context.Background()),
		Location: t.TempDir(),
	}

	before := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	conf := NewWatchConfiguration(builders.NewDependencies("test"))
	if err := base.SetupWatcher(ctx, conf, func(code.Change) error { return nil }); err != nil {
		t.Fatalf("SetupWatcher: %v", err)
	}

	cancel()

	// Poll until the two watcher goroutines settle back to baseline. A leaked
	// debounce goroutine keeps the count above baseline indefinitely.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutines did not settle after cancel: before=%d after=%d", before, runtime.NumGoroutine())
}
