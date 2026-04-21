package manager

import (
	"strings"
	"testing"
)

// TestStderrCapacityBig ensures we keep the bigger buffer. Specifically
// guards against accidental revert to the prior 4KB — a Go stacktrace
// is routinely 8-16KB, which 4KB would silently truncate and lose the
// top frames (the useful ones).
func TestStderrCapacityBig(t *testing.T) {
	if stderrCapacity < 32*1024 {
		t.Errorf("stderrCapacity = %d; should be at least 32KB to hold a typical Go panic traceback", stderrCapacity)
	}
}

// TestRingBuffer_SmallWrite_BelowCapacity exercises the happy path: a
// single write that fits.
func TestRingBuffer_SmallWrite_BelowCapacity(t *testing.T) {
	r := newRingBuffer(64)
	if _, err := r.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got := r.String(); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

// TestRingBuffer_OversizedWrite_KeepsTail asserts the documented
// behavior: writing more than capacity keeps the tail.
func TestRingBuffer_OversizedWrite_KeepsTail(t *testing.T) {
	cap := 16
	r := newRingBuffer(cap)
	big := strings.Repeat("A", 10) + strings.Repeat("B", 20)
	if _, err := r.Write([]byte(big)); err != nil {
		t.Fatal(err)
	}
	got := r.String()
	if len(got) != cap {
		t.Errorf("len = %d, want %d", len(got), cap)
	}
	// The last cap bytes should all be Bs (overwrote the As).
	if got != strings.Repeat("B", cap) {
		t.Errorf("tail should be all B, got %q", got)
	}
}

// TestRingBuffer_WrapAround covers the core circular-buffer property:
// multiple writes crossing the capacity boundary preserve the most
// recent bytes.
func TestRingBuffer_WrapAround(t *testing.T) {
	r := newRingBuffer(8)
	// First write fills exactly to capacity.
	_, _ = r.Write([]byte("ABCDEFGH"))
	if got := r.String(); got != "ABCDEFGH" {
		t.Errorf("after fill: got %q", got)
	}
	// Second write wraps.
	_, _ = r.Write([]byte("12345"))
	// Expected tail: "DEFGH" from first + "12345" from second = "DEFGH12345"?
	// Capacity is 8, so we keep the last 8 bytes: "FGH12345".
	if got := r.String(); got != "FGH12345" {
		t.Errorf("after wrap: got %q, want FGH12345", got)
	}
}

// TestRingBuffer_Concurrency proves the Write/String locking works —
// writers don't race with the reader.
func TestRingBuffer_Concurrency(t *testing.T) {
	r := newRingBuffer(1024)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			_, _ = r.Write([]byte("payload"))
		}
		close(done)
	}()
	// Interleave reads. Shouldn't panic or deadlock.
	for i := 0; i < 1000; i++ {
		_ = r.String()
	}
	<-done
}
