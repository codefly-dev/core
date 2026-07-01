package base

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestForwardLines_PreservesNewlinesPerLine proves each newline-terminated
// line is written as its own Write with the separator intact — the boundary
// wool's per-line prefixing relies on.
func TestForwardLines_PreservesNewlinesPerLine(t *testing.T) {
	src := "alpha\nbeta\ngamma\n"
	w := &recordingWriter{}
	forwardLines(strings.NewReader(src), w)

	if got := w.buf.String(); got != src {
		t.Fatalf("content mismatch:\n got %q\nwant %q", got, src)
	}
	want := []string{"alpha\n", "beta\n", "gamma\n"}
	if len(w.writes) != len(want) {
		t.Fatalf("expected %d per-line writes, got %d: %q", len(want), len(w.writes), w.writes)
	}
	for i, ln := range want {
		if w.writes[i] != ln {
			t.Fatalf("write %d = %q, want %q", i, w.writes[i], ln)
		}
	}
}

// TestForwardLines_OversizedLineNotTruncated is the regression guard for #29:
// a single line larger than bufio.Scanner's 1MiB token cap used to make the
// forwarder drop that line AND everything after it. ReadBytes forwards the
// whole stream regardless of line length.
func TestForwardLines_OversizedLineNotTruncated(t *testing.T) {
	huge := strings.Repeat("x", 4*1024*1024) // 4MiB, well past the old 1MiB cap
	src := huge + "\n" + "after-the-blob\n"
	var w bytes.Buffer
	forwardLines(strings.NewReader(src), &w)

	if w.String() != src {
		t.Fatalf("oversized line truncated: got %d bytes, want %d", w.Len(), len(src))
	}
	if !strings.Contains(w.String(), "after-the-blob\n") {
		t.Fatal("output after the oversized line was lost")
	}
}

// TestForwardLines_NoTrailingNewline forwards a final line that the child
// emitted without a terminating newline (e.g. a prompt) without dropping it.
func TestForwardLines_NoTrailingNewline(t *testing.T) {
	src := "line\npartial"
	var w bytes.Buffer
	forwardLines(strings.NewReader(src), &w)
	if w.String() != src {
		t.Fatalf("got %q, want %q", w.String(), src)
	}
}

// TestForwardLines_DrainsAfterWriteError proves that when the downstream
// writer fails, forwardLines keeps reading r to EOF so the child never blocks
// on a full pipe while the forwarder is about to close the read-end.
func TestForwardLines_DrainsAfterWriteError(t *testing.T) {
	src := strings.NewReader("first\nsecond\nthird\n")
	forwardLines(src, failingWriter{})

	rest, err := io.ReadAll(src)
	if err != nil {
		t.Fatalf("reading remainder: %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("reader not drained after write error: %q remained", rest)
	}
}

type recordingWriter struct {
	buf    bytes.Buffer
	writes []string
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.writes = append(w.writes, string(p))
	return w.buf.Write(p)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}
