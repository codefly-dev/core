package code

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/shared"
)

// TestWatcher_NestedAtomicRenameSave is the regression for hot-reload silently
// missing pkg/** edits: nested directories must be watched, and an atomic-rename
// save (write a temp file → rename it over the original — how most editors save)
// must fire a Change. The old file-level, Write-only watcher dropped both.
func TestWatcher_NestedAtomicRenameSave(t *testing.T) {
	base := t.TempDir()
	pkgDir := filepath.Join(base, "code", "pkg", "intake")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(pkgDir, "intake.go")
	if err := os.WriteFile(target, []byte("package intake\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := builders.NewDependencies("test",
		builders.NewDependency("code").WithPathSelect(shared.NewSelect("*.go")),
	)

	events := make(chan Change, 16)
	ctx := context.Background()
	w, err := NewWatcher(ctx, events, base, deps)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	go w.Start(ctx)
	time.Sleep(150 * time.Millisecond) // let fsnotify register the dir watches

	// Atomic-rename save of the nested file.
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte("package intake\n// edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, target); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-events:
		if filepath.Base(ev.Path) != "intake.go" {
			t.Fatalf("change path = %q, want a nested intake.go", ev.Path)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no change event for a nested atomic-rename save — hot-reload silently misses pkg/** edits")
	}
}
