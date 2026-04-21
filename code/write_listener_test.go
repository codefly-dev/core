package code

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// writeEvent captures one call to the WriteListener so tests can
// assert on the sequence of mutations that were reported.
type writeEvent struct {
	kind     string
	path     string
	prevPath string
	content  string
}

// recordingListener builds a WriteListener that appends every call
// into a thread-safe slice. Returned alongside a drain function so
// tests can snapshot the events without a race.
func recordingListener() (WriteListener, func() []writeEvent) {
	var mu sync.Mutex
	var events []writeEvent
	fn := func(_ context.Context, kind, path, prevPath string, content []byte) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, writeEvent{
			kind:     kind,
			path:     path,
			prevPath: prevPath,
			content:  string(content),
		})
		return nil
	}
	drain := func() []writeEvent {
		mu.Lock()
		defer mu.Unlock()
		out := make([]writeEvent, len(events))
		copy(out, events)
		return out
	}
	return fn, drain
}

// newListenerServer returns a DefaultCodeServer rooted at a temp dir
// with a recording listener already installed. Every test case below
// uses this so the setup stays uniform.
func newListenerServer(t *testing.T) (*DefaultCodeServer, func() []writeEvent, string) {
	t.Helper()
	dir := t.TempDir()
	s := NewDefaultCodeServer(dir)
	listener, drain := recordingListener()
	s.SetWriteListener(listener)
	return s, drain, dir
}

// ──────────────────────────────────────────────────────────
// Write → listener sees (kind="write", path, content)
// ──────────────────────────────────────────────────────────

func TestWriteListener_WriteFile(t *testing.T) {
	s, drain, dir := newListenerServer(t)

	_, err := s.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_WriteFile{
			WriteFile: &codev0.WriteFileRequest{Path: "foo.txt", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	events := drain()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	got := events[0]
	if got.kind != "write" {
		t.Errorf("kind = %q, want %q", got.kind, "write")
	}
	if got.path != "foo.txt" {
		t.Errorf("path = %q, want %q", got.path, "foo.txt")
	}
	if got.content != "hello" {
		t.Errorf("content = %q, want %q", got.content, "hello")
	}

	// Sanity: the file really landed on disk.
	data, _ := os.ReadFile(filepath.Join(dir, "foo.txt"))
	if string(data) != "hello" {
		t.Errorf("disk content = %q, want %q", data, "hello")
	}
}

// ──────────────────────────────────────────────────────────
// Create → listener sees (kind="create", path, content)
// ──────────────────────────────────────────────────────────

func TestWriteListener_CreateFile(t *testing.T) {
	s, drain, _ := newListenerServer(t)

	_, err := s.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_CreateFile{
			CreateFile: &codev0.CreateFileRequest{Path: "new.go", Content: "package x"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	events := drain()
	if len(events) != 1 || events[0].kind != "create" {
		t.Fatalf("expected 1 create event, got %+v", events)
	}
	if events[0].content != "package x" {
		t.Errorf("content mismatch: %q", events[0].content)
	}
}

// ──────────────────────────────────────────────────────────
// Delete → listener sees (kind="delete", path, nil content)
// ──────────────────────────────────────────────────────────

func TestWriteListener_DeleteFile(t *testing.T) {
	s, drain, dir := newListenerServer(t)

	// Seed a file, then delete it via the dispatcher.
	_ = os.WriteFile(filepath.Join(dir, "victim.txt"), []byte("bye"), 0o644)

	_, err := s.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_DeleteFile{
			DeleteFile: &codev0.DeleteFileRequest{Path: "victim.txt"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	events := drain()
	if len(events) != 1 || events[0].kind != "delete" {
		t.Fatalf("expected 1 delete event, got %+v", events)
	}
	if events[0].path != "victim.txt" {
		t.Errorf("path = %q", events[0].path)
	}
	if events[0].content != "" {
		t.Errorf("delete content should be empty, got %q", events[0].content)
	}
}

// ──────────────────────────────────────────────────────────
// Move → listener sees (kind="move", new path, old path, content)
// ──────────────────────────────────────────────────────────

func TestWriteListener_MoveFile(t *testing.T) {
	s, drain, dir := newListenerServer(t)
	_ = os.WriteFile(filepath.Join(dir, "old.txt"), []byte("payload"), 0o644)

	_, err := s.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_MoveFile{
			MoveFile: &codev0.MoveFileRequest{OldPath: "old.txt", NewPath: "renamed.txt"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	events := drain()
	if len(events) != 1 || events[0].kind != "move" {
		t.Fatalf("expected 1 move event, got %+v", events)
	}
	got := events[0]
	if got.path != "renamed.txt" || got.prevPath != "old.txt" {
		t.Errorf("path routing wrong: path=%q prev=%q", got.path, got.prevPath)
	}
	if got.content != "payload" {
		t.Errorf("move should carry new content; got %q", got.content)
	}
}

// ──────────────────────────────────────────────────────────
// Failed write → NO event
// ──────────────────────────────────────────────────────────

func TestWriteListener_NotFiredOnFailedDelete(t *testing.T) {
	s, drain, _ := newListenerServer(t)

	// Delete a file that doesn't exist. Should return an error inside
	// the response (not a go error), and should NOT fire the listener.
	resp, err := s.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_DeleteFile{
			DeleteFile: &codev0.DeleteFileRequest{Path: "nope.txt"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if df := resp.GetDeleteFile(); df == nil || df.Success {
		t.Errorf("delete of missing file should fail, got %+v", df)
	}

	if events := drain(); len(events) != 0 {
		t.Errorf("expected no events on failed delete, got %+v", events)
	}
}

// ──────────────────────────────────────────────────────────
// Listener is optional — nil listener is safe
// ──────────────────────────────────────────────────────────

func TestWriteListener_NilListenerIsNoOp(t *testing.T) {
	dir := t.TempDir()
	s := NewDefaultCodeServer(dir) // no listener installed

	_, err := s.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_WriteFile{
			WriteFile: &codev0.WriteFileRequest{Path: "x.txt", Content: "y"},
		},
	})
	if err != nil {
		t.Fatalf("nil listener should not break WriteFile: %v", err)
	}
	// Verify the file landed.
	data, _ := os.ReadFile(filepath.Join(dir, "x.txt"))
	if string(data) != "y" {
		t.Errorf("content = %q", data)
	}
}

// ──────────────────────────────────────────────────────────
// Listener errors do NOT break the mutation
// ──────────────────────────────────────────────────────────

func TestWriteListener_ErrorsDoNotFailMutation(t *testing.T) {
	dir := t.TempDir()
	s := NewDefaultCodeServer(dir)
	s.SetWriteListener(func(_ context.Context, _, _, _ string, _ []byte) error {
		return fmt.Errorf("simulated LSP not ready")
	})

	resp, err := s.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_WriteFile{
			WriteFile: &codev0.WriteFileRequest{Path: "a.txt", Content: "ok"},
		},
	})
	if err != nil {
		t.Fatalf("listener error should not return a go error: %v", err)
	}
	if wf := resp.GetWriteFile(); wf == nil || !wf.Success {
		t.Errorf("listener error should not flip Success=false, got %+v", wf)
	}
	// File should still be on disk.
	data, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(data) != "ok" {
		t.Errorf("content = %q", data)
	}
}

// ──────────────────────────────────────────────────────────
// Listener panics do NOT break the mutation
// ──────────────────────────────────────────────────────────

func TestWriteListener_PanicsDoNotBreakMutation(t *testing.T) {
	dir := t.TempDir()
	s := NewDefaultCodeServer(dir)
	s.SetWriteListener(func(_ context.Context, _, _, _ string, _ []byte) error {
		panic("buggy listener")
	})

	resp, err := s.Execute(context.Background(), &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_WriteFile{
			WriteFile: &codev0.WriteFileRequest{Path: "panic.txt", Content: "ok"},
		},
	})
	if err != nil {
		t.Fatalf("panic in listener should not return go error: %v", err)
	}
	if wf := resp.GetWriteFile(); wf == nil || !wf.Success {
		t.Errorf("listener panic should not flip Success=false: %+v", wf)
	}
}
