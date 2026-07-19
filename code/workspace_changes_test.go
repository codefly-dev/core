package code

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceChangeMonitorObservesExternalFileLifecycle(t *testing.T) {
	root := t.TempDir()
	monitor, err := NewWorkspaceChangeMonitor(root, WithWorkspaceChangeSourceID("source-a"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = monitor.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	subscription, err := monitor.Subscribe(ctx, WorkspaceChangeCursor{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(subscription.Close)

	file := filepath.Join(root, "pkg", "a.go")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	// A newly-created directory forces a rescan because files can appear before
	// recursive watches are installed. That is an explicit correctness event,
	// not a dropped notification.
	first := receiveWorkspaceEvent(t, subscription)
	assertWorkspaceRescan(t, first, "directory_created")
	if err := os.WriteFile(file, []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := receiveWorkspaceChange(t, subscription, "pkg/a.go")
	if created.Kind != WorkspaceChangeCreate && created.Kind != WorkspaceChangeWrite {
		t.Fatalf("create change=%+v", created)
	}
	if err := os.WriteFile(file, []byte("package pkg\nconst A = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	written := receiveWorkspaceChange(t, subscription, "pkg/a.go")
	if written.Kind != WorkspaceChangeWrite {
		t.Fatalf("write change=%+v", written)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	removed := receiveWorkspaceChange(t, subscription, "pkg/a.go")
	if removed.Kind != WorkspaceChangeRemove {
		t.Fatalf("remove change=%+v", removed)
	}
}

func TestWorkspaceChangeMonitorReplaysAndRescansAcrossCursorBoundaries(t *testing.T) {
	monitor, err := NewWorkspaceChangeMonitor(t.TempDir(),
		WithWorkspaceChangeSourceID("source-a"), WithWorkspaceChangeReplayLimit(2))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = monitor.Close() })
	monitor.publish([]WorkspaceChange{{Kind: WorkspaceChangeWrite, Path: "a.go"}})
	firstSequence := monitor.sequence
	monitor.publish([]WorkspaceChange{{Kind: WorkspaceChangeWrite, Path: "b.go"}})

	replay, err := monitor.Subscribe(t.Context(), WorkspaceChangeCursor{SourceID: "source-a", Sequence: firstSequence})
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	event := receiveWorkspaceEvent(t, replay)
	if event.Sequence != firstSequence+1 || len(event.Changes) != 1 || event.Changes[0].Path != "b.go" {
		t.Fatalf("replayed event=%+v", event)
	}

	monitor.publish([]WorkspaceChange{{Kind: WorkspaceChangeWrite, Path: "c.go"}})
	monitor.publish([]WorkspaceChange{{Kind: WorkspaceChangeWrite, Path: "d.go"}})
	stale, err := monitor.Subscribe(t.Context(), WorkspaceChangeCursor{SourceID: "source-a", Sequence: firstSequence})
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Close()
	assertWorkspaceRescan(t, receiveWorkspaceEvent(t, stale), "replay_window_exceeded")

	foreign, err := monitor.Subscribe(t.Context(), WorkspaceChangeCursor{SourceID: "old-source", Sequence: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	assertWorkspaceRescan(t, receiveWorkspaceEvent(t, foreign), "source_changed")
}

func TestWorkspaceChangeMonitorSlowConsumerFailsAndCanReplay(t *testing.T) {
	monitor, err := NewWorkspaceChangeMonitor(t.TempDir(),
		WithWorkspaceChangeSourceID("source-a"), WithWorkspaceChangeSubscriberBuffer(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = monitor.Close() })
	subscription, err := monitor.Subscribe(t.Context(), WorkspaceChangeCursor{})
	if err != nil {
		t.Fatal(err)
	}
	monitor.publish([]WorkspaceChange{{Kind: WorkspaceChangeWrite, Path: "a.go"}})
	monitor.publish([]WorkspaceChange{{Kind: WorkspaceChangeWrite, Path: "b.go"}})
	first, err := subscription.Recv()
	if err != nil || first.Sequence != 1 {
		t.Fatalf("first event=%+v err=%v", first, err)
	}
	if _, err := subscription.Recv(); !errors.Is(err, ErrWorkspaceChangeStreamSlow) {
		t.Fatalf("slow consumer error=%v", err)
	}

	replay, err := monitor.Subscribe(t.Context(), WorkspaceChangeCursor{SourceID: "source-a", Sequence: first.Sequence})
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	second, err := replay.Recv()
	if err != nil || second.Sequence != 2 || second.Changes[0].Path != "b.go" {
		t.Fatalf("replayed second=%+v err=%v", second, err)
	}
}

func TestWorkspaceChangeMonitorCloseUnblocksSubscriber(t *testing.T) {
	monitor, err := NewWorkspaceChangeMonitor(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := monitor.Subscribe(context.Background(), WorkspaceChangeCursor{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := subscription.Recv()
		done <- err
	}()
	if err := monitor.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrWorkspaceChangeMonitorClosed) {
			t.Fatalf("Recv error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not unblock on monitor close")
	}
}

func receiveWorkspaceEvent(t *testing.T, subscription *WorkspaceChangeSubscription) WorkspaceChangeEvent {
	t.Helper()
	type result struct {
		event WorkspaceChangeEvent
		err   error
	}
	done := make(chan result, 1)
	go func() {
		event, err := subscription.Recv()
		done <- result{event: event, err: err}
	}()
	select {
	case value := <-done:
		if value.err != nil {
			t.Fatalf("receive workspace event: %v", value.err)
		}
		return value.event
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for workspace event")
		return WorkspaceChangeEvent{}
	}
}

func receiveWorkspaceChange(t *testing.T, subscription *WorkspaceChangeSubscription, path string) WorkspaceChange {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		event := receiveWorkspaceEvent(t, subscription)
		for _, change := range event.Changes {
			if change.Path == path {
				return change
			}
		}
	}
	t.Fatalf("timed out waiting for workspace change %q", path)
	return WorkspaceChange{}
}

func assertWorkspaceRescan(t *testing.T, event WorkspaceChangeEvent, reason string) {
	t.Helper()
	if len(event.Changes) != 1 || event.Changes[0].Kind != WorkspaceChangeRescan || event.Changes[0].Reason != reason {
		t.Fatalf("rescan event=%+v want reason=%q", event, reason)
	}
}
