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
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

func TestDockerImageBuildDigestIsRequestScoped(t *testing.T) {
	base := &Base{
		Identity: &resources.ServiceIdentity{Module: "module", Name: "service"},
		Service:  &resources.Service{Version: "1.2.3"},
	}
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	taggedBuild := &builderv0.DockerBuildContext{DockerRepository: "registry.example.com"}
	digestBuild := &builderv0.DockerBuildContext{DockerRepository: "registry.example.com", ImageDigest: digest}

	base.SetDefaultDockerImage(taggedBuild)

	pinned := base.DockerImage(digestBuild)
	require.Equal(t, "registry.example.com/module/service@"+digest, pinned.FullName())
	require.Equal(t, "registry.example.com/module/service:1.2.3", base.DockerImage(taggedBuild).FullName())
	require.Equal(t, "registry.example.com/module/service@"+digest, pinned.FullName())
}

// TestUniqueWithWorkspaceSeparatesInvocationScopes pins the agent-side half of
// dependency session isolation. UniqueWithWorkspace names the state directory,
// container and log root an agent creates for a service, so two concurrent
// invocations of the same service in the same workspace stay separate only
// while their naming scopes differ.
func TestUniqueWithWorkspaceSeparatesInvocationScopes(t *testing.T) {
	ctx := context.Background()
	scoped := func(scope string) string {
		base := &Base{
			Identity:    &resources.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "service"},
			Wool:        wool.Get(ctx),
			Environment: &basev0.Environment{NamingScope: scope},
		}
		return base.UniqueWithWorkspace()
	}

	unscoped := scoped("")
	first := scoped("s0123456789ab")
	second := scoped("s0123456789ac")

	require.NotEqual(t, first, second)
	require.NotEqual(t, unscoped, first)
	require.Contains(t, first, "s0123456789ab")
}

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

// TestStopWatcher_ExitsWithoutContextCancel is the regression for the actual
// production teardown path: Runtime.Stop calls StopWatcher() (it must NOT close
// s.Events — that second close raced the watcher into a "close of closed
// channel" panic). The context passed to SetupWatcher is NOT cancelled here, so
// this proves StopWatcher alone ends both goroutines via its stored cancelFunc,
// whose `defer close(Events)` unblocks the debounce consumer.
func TestStopWatcher_ExitsWithoutContextCancel(t *testing.T) {
	base := &Base{
		Wool:     wool.Get(context.Background()),
		Location: t.TempDir(),
	}

	// Background context — the test never cancels it; only StopWatcher can.
	conf := NewWatchConfiguration(builders.NewDependencies("test"))
	require.NoError(t, base.SetupWatcher(context.Background(), conf, func(code.Change) error { return nil }))

	waitWatcherGoroutines(t, 2, time.Second) // Start loop + debounce goroutine

	base.StopWatcher()

	waitWatcherGoroutines(t, 0, 3*time.Second)

	// Idempotent: a second StopWatcher is safe (Runtime.Stop may run twice), and
	// StopWatcher on a Base that never set up a watcher must also be a no-op.
	base.StopWatcher()
	(&Base{Wool: wool.Get(context.Background())}).StopWatcher()
}

// save performs an atomic-rename save (write temp → rename over original),
// which is how most editors save and what the directory watcher observes.
func save(t *testing.T, target, content string) {
	t.Helper()
	tmp := target + ".tmp"
	require.NoError(t, os.WriteFile(tmp, []byte(content), 0o644))
	require.NoError(t, os.Rename(tmp, target))
}

// A healthy sibling cannot tear down the watcher while a different subtree
// remains unreadable. The pending recovery must survive to observe its repair.
func TestSetupWatcherDoesNotRestartWhileCoverageDegraded(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	incoming := filepath.Join(root, "incoming")
	require.NoError(t, os.Mkdir(source, 0755))
	require.NoError(t, os.Mkdir(incoming, 0755))
	require.NoError(t, os.Chmod(incoming, 0300))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(source, "incoming"), 0755); _ = os.Chmod(incoming, 0755) })
	if _, err := os.ReadDir(incoming); err == nil {
		t.Skip("process bypasses directory permissions")
	}
	require.NoError(t, os.WriteFile(filepath.Join(source, "sibling.go"), []byte("package sibling"), 0644))
	base := &Base{Wool: wool.Get(t.Context()), Location: root}
	callbacks := make(chan code.Change, 8)
	require.NoError(t, base.SetupWatcher(t.Context(), NewWatchConfiguration(builders.NewDependencies("recovery")).WithTrees(code.WatchTree{Root: source}), func(c code.Change) error { callbacks <- c; return nil }))
	t.Cleanup(base.StopWatcher)
	require.NoError(t, os.Rename(incoming, filepath.Join(source, "incoming")))
	require.Eventually(t, func() bool { return base.Watcher.Err() != nil }, time.Second, 10*time.Millisecond)
	// Observe four further real failed registrations. The next bounded backoff
	// exceeds debounce, making the sibling callback race deterministic.
	for attempt := 0; attempt < 4; attempt++ {
		previous := base.Watcher.Err()
		require.Eventually(t, func() bool { current := base.Watcher.Err(); return current != nil && current != previous }, 6*time.Second, 5*time.Millisecond)
	}
	require.NoError(t, os.WriteFile(filepath.Join(source, "sibling.go"), []byte("package sibling\n// changed"), 0644))
	select {
	case c := <-callbacks:
		t.Fatalf("premature restart while coverage is degraded: %#v", c)
	case <-time.After(WatchDebounce + 150*time.Millisecond):
	}
	require.Error(t, base.Watcher.Err())
	require.NoError(t, os.Chmod(filepath.Join(source, "incoming"), 0755))
	select {
	case c := <-callbacks:
		require.True(t, c.Rescan, "repair must rediscover the whole input set")
	case <-time.After(7 * time.Second):
		t.Fatal("repair did not resume callback")
	}
	require.NoError(t, base.Watcher.Err())
	require.NoError(t, os.WriteFile(filepath.Join(source, "incoming", "next.go"), []byte("package next"), 0644))
	select {
	case c := <-callbacks:
		require.Equal(t, filepath.Join("source", "incoming", "next.go"), c.Path)
	case <-time.After(2 * time.Second):
		t.Fatal("repaired subtree is not watched")
	}
}
