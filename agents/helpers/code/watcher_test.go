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

// TestWatcher_SkipsTransientTempFiles is the regression for hot-reload churn:
// a service has a NAMED-FILE dependency with no path select (service.codefly.yaml),
// whose Keep() matches every path. Because we watch directories, an editor's
// `foo.go.tmp` write was matched by that no-select dep and triggered a spurious
// rebuild MID-BUILD — and that churn raced the restart and wedged the daemon.
// Temp files must be ignored; the real `.go` save must still fire exactly once.
func TestWatcher_SkipsTransientTempFiles(t *testing.T) {
	base := t.TempDir()
	pkgDir := filepath.Join(base, "code", "pkg", "intake")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A no-path-select named-file dependency (matches everything), exactly the
	// service.codefly.yaml dependency that exposed the bug, alongside the *.go one.
	deps := builders.NewDependencies("test",
		builders.NewDependency("service.codefly.yaml"),
		builders.NewDependency("code").WithPathSelect(shared.NewSelect("*.go")),
	)

	events := make(chan Change, 16)
	ctx := context.Background()
	w, err := NewWatcher(ctx, events, base, deps)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	go w.Start(ctx)
	time.Sleep(150 * time.Millisecond)

	// A bare temp-file write (no following rename) must NOT fire a change.
	if err := os.WriteFile(filepath.Join(pkgDir, "intake.go.tmp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A vim swap file must NOT fire a change either.
	if err := os.WriteFile(filepath.Join(pkgDir, ".intake.go.swp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		t.Fatalf("transient temp file fired a spurious change: %q", ev.Path)
	case <-time.After(600 * time.Millisecond):
		// good — no spurious rebuild
	}

	// A real source write MUST still fire exactly one change.
	if err := os.WriteFile(filepath.Join(pkgDir, "real.go"), []byte("package intake\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if filepath.Base(ev.Path) != "real.go" {
			t.Fatalf("change path = %q, want real.go", ev.Path)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("real .go write produced no change — hot-reload broken")
	}
}
