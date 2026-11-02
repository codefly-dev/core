package code

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

func recoveryWatcher(t *testing.T, base string, trees ...WatchTree) (*Watcher, <-chan Change) {
	t.Helper()
	events := make(chan Change, 128)
	ctx, cancel := context.WithCancel(context.Background())
	w, err := NewWatcher(ctx, events, base, nil, trees...)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { defer close(done); w.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("watcher did not stop")
		}
	})
	return w, events
}

func awaitWatch(t *testing.T, events <-chan Change, predicate func(Change) bool) Change {
	t.Helper()
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case change, ok := <-events:
			require.True(t, ok, "watch closed before expected observation")
			if predicate(change) {
				return change
			}
		case <-deadline.C:
			t.Fatal("missing filesystem observation")
			return Change{}
		}
	}
}
func drainWatch(events <-chan Change) {
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

func TestWatcherExplicitTreeRecoversMissingAndRecreatedRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "missing", "source")
	_, events := recoveryWatcher(t, base, WatchTree{Root: root})
	require.NoError(t, os.MkdirAll(root, 0755))
	awaitWatch(t, events, func(c Change) bool { return c.Rescan })
	target := filepath.Join(root, "schema.json")
	require.NoError(t, os.WriteFile(target, []byte("first"), 0644))
	awaitWatch(t, events, func(c Change) bool { return c.Path == filepath.Join("missing", "source", "schema.json") })
	require.NoError(t, os.RemoveAll(filepath.Join(base, "missing")))
	awaitWatch(t, events, func(c Change) bool { return c.Rescan })
	require.NoError(t, os.MkdirAll(root, 0755))
	awaitWatch(t, events, func(c Change) bool { return c.Rescan })
	require.NoError(t, os.WriteFile(target, []byte("repaired"), 0644))
	// A recreated populated tree is a full invalidation; leaf events before
	// registration need not be replayed. The owner reads the current input.
	awaitWatch(t, events, func(c Change) bool { return c.Rescan || c.Path == filepath.Join("missing", "source", "schema.json") })
	actual, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "repaired", string(actual))
	require.NoError(t, os.WriteFile(target, []byte("subsequent"), 0644))
	awaitWatch(t, events, func(c Change) bool { return c.Path == filepath.Join("missing", "source", "schema.json") })
}

func TestWatcherRegistersPopulatedIncomingTreeAndDeletion(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "source")
	incoming := filepath.Join(base, "incoming")
	require.NoError(t, os.MkdirAll(root, 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(incoming, "deep"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(incoming, "deep", "schema.json"), []byte("one"), 0644))
	_, events := recoveryWatcher(t, base, WatchTree{Root: root})
	require.NoError(t, os.Rename(incoming, filepath.Join(root, "new")))
	awaitWatch(t, events, func(c Change) bool { return c.Rescan && c.Path == filepath.Join("source", "new") })
	target := filepath.Join(root, "new", "deep", "schema.json")
	require.NoError(t, os.WriteFile(target, []byte("two"), 0644))
	awaitWatch(t, events, func(c Change) bool { return c.Path == filepath.Join("source", "new", "deep", "schema.json") })
	require.NoError(t, os.Remove(target))
	awaitWatch(t, events, func(c Change) bool { return c.Path == filepath.Join("source", "new", "deep", "schema.json") })
}

func TestWatcherExplicitExclusionsDoNotHideIgnoredInputs(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "source")
	for _, dir := range []string{"node_modules", "ignored", "dist"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored/\n"), 0644))
	_, events := recoveryWatcher(t, base, WatchTree{Root: root, Exclude: func(p string) bool { return filepath.Base(p) == "dist" }})
	for _, name := range []string{"node_modules/embedded.json", "ignored/named.tgz"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("input"), 0644))
		awaitWatch(t, events, func(c Change) bool { return c.Path == filepath.Join("source", name) })
	}
	drainWatch(events)
	require.NoError(t, os.WriteFile(filepath.Join(root, "dist", "output.js"), []byte("output"), 0644))
	select {
	case c := <-events:
		t.Fatalf("excluded output produced %#v", c)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestWatcherLegacyTreeKeepsScopeAfterRemoval(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "source")
	require.NoError(t, os.MkdirAll(root, 0755))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Change, 64)
	w, err := NewWatcher(ctx, events, base, builders.NewDependencies("go", builders.NewDependency("source").WithPathSelect(shared.NewSelect("*.go"))))
	require.NoError(t, err)
	go w.Start(ctx)
	require.NoError(t, os.RemoveAll(root))
	awaitWatch(t, events, func(c Change) bool { return c.Rescan })
	require.NoError(t, os.MkdirAll(root, 0755))
	awaitWatch(t, events, func(c Change) bool { return c.Rescan })
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0644))
	awaitWatch(t, events, func(c Change) bool { return c.Path == filepath.Join("source", "main.go") })
}

func TestWatcherClosedFilesystemReportsDegradedState(t *testing.T) {
	base := t.TempDir()
	w, events := recoveryWatcher(t, base, WatchTree{Root: base})
	require.NoError(t, w.watcher.Close()) // a real underlying resource failure, not a fake error stream
	awaitWatch(t, events, func(c Change) bool { return c.Err != nil && c.Rescan })
	require.Error(t, w.Err())
}

func TestWatcherRegistrationFailureRecoversAfterPermissionsRepair(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "source")
	incoming := filepath.Join(base, "incoming")
	require.NoError(t, os.MkdirAll(root, 0755))
	require.NoError(t, os.MkdirAll(incoming, 0755))
	require.NoError(t, os.Chmod(incoming, 0300))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "incoming"), 0755) })
	if _, err := os.ReadDir(incoming); err == nil {
		t.Skip("process can bypass directory permissions")
	}
	w, events := recoveryWatcher(t, base, WatchTree{Root: root})
	require.NoError(t, os.Rename(incoming, filepath.Join(root, "incoming")))
	awaitWatch(t, events, func(c Change) bool { return c.Err != nil })
	require.Error(t, w.Err())
	require.NoError(t, os.Chmod(filepath.Join(root, "incoming"), 0755))
	awaitWatch(t, events, func(c Change) bool { return c.Rescan && c.Err == nil })
	require.NoError(t, w.Err())
	require.NoError(t, os.WriteFile(filepath.Join(root, "incoming", "repaired.json"), []byte("ok"), 0644))
	awaitWatch(t, events, func(c Change) bool { return c.Path == filepath.Join("source", "incoming", "repaired.json") })
}

func TestWatcherExplicitSymlinkRootRefusesIncompleteCoverage(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	link := filepath.Join(base, "link")
	require.NoError(t, os.Mkdir(target, 0755))
	require.NoError(t, os.Symlink(target, link))
	_, err := NewWatcher(t.Context(), make(chan Change), base, nil, WatchTree{Root: link})
	require.ErrorContains(t, err, "symbolic link")
}
