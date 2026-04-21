package code

import (
	"strings"
	"testing"
)

func TestByteLRU_PutGet(t *testing.T) {
	c := NewByteLRU(1024)
	c.Put("a", []byte("hello"))
	c.Put("b", []byte("world"))

	if got := c.Get("a"); string(got) != "hello" {
		t.Errorf("Get(a) = %q, want 'hello'", got)
	}
	if got := c.Get("b"); string(got) != "world" {
		t.Errorf("Get(b) = %q, want 'world'", got)
	}
	if got := c.Get("c"); got != nil {
		t.Errorf("Get(c) = %q, want nil", got)
	}
}

func TestByteLRU_Eviction(t *testing.T) {
	c := NewByteLRU(100) // 100 bytes capacity

	c.Put("a", []byte(strings.Repeat("x", 50)))
	c.Put("b", []byte(strings.Repeat("y", 50)))

	if c.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", c.Len())
	}

	// Adding "c" (50 bytes) should evict "a" (LRU)
	c.Put("c", []byte(strings.Repeat("z", 50)))

	if c.Get("a") != nil {
		t.Error("expected 'a' to be evicted")
	}
	if c.Get("b") == nil {
		t.Error("expected 'b' to still exist")
	}
	if c.Get("c") == nil {
		t.Error("expected 'c' to exist")
	}
}

func TestByteLRU_LRUOrder(t *testing.T) {
	c := NewByteLRU(100)

	c.Put("a", []byte(strings.Repeat("x", 40)))
	c.Put("b", []byte(strings.Repeat("y", 40)))

	// Access "a" to make it recently used
	c.Get("a")

	// Adding "c" should evict "b" (now LRU), not "a"
	c.Put("c", []byte(strings.Repeat("z", 40)))

	if c.Get("a") == nil {
		t.Error("expected 'a' to survive (recently accessed)")
	}
	if c.Get("b") != nil {
		t.Error("expected 'b' to be evicted (LRU)")
	}
}

func TestByteLRU_Update(t *testing.T) {
	c := NewByteLRU(1024)

	c.Put("a", []byte("short"))
	if c.Size() != 5 {
		t.Errorf("size = %d, want 5", c.Size())
	}

	c.Put("a", []byte("longer value"))
	if c.Size() != 12 {
		t.Errorf("size = %d, want 12", c.Size())
	}

	if got := c.Get("a"); string(got) != "longer value" {
		t.Errorf("Get(a) = %q, want 'longer value'", got)
	}
}

func TestByteLRU_Invalidate(t *testing.T) {
	c := NewByteLRU(1024)
	c.Put("a", []byte("hello"))

	c.Invalidate("a")

	if c.Get("a") != nil {
		t.Error("expected nil after invalidate")
	}
	if c.Size() != 0 {
		t.Errorf("size = %d, want 0", c.Size())
	}
}

func TestByteLRU_Clear(t *testing.T) {
	c := NewByteLRU(1024)
	c.Put("a", []byte("hello"))
	c.Put("b", []byte("world"))

	c.Clear()

	if c.Len() != 0 {
		t.Errorf("Len = %d, want 0", c.Len())
	}
	if c.Size() != 0 {
		t.Errorf("Size = %d, want 0", c.Size())
	}
}

func TestByteLRU_SkipLargeEntry(t *testing.T) {
	c := NewByteLRU(1024 * 1024) // 1MB capacity
	c.MaxEntrySize = 100          // but max entry is 100 bytes

	c.Put("big", []byte(strings.Repeat("x", 200)))

	if c.Get("big") != nil {
		t.Error("expected large entry to be skipped")
	}
}

func TestByteLRU_SkipBinary(t *testing.T) {
	c := NewByteLRU(1024)

	c.Put("bin", []byte{0x89, 0x50, 0x4E, 0x47, 0x00}) // PNG-like with null byte

	if c.Get("bin") != nil {
		t.Error("expected binary file to be skipped")
	}
}

func TestIsBinary(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		binary bool
	}{
		{"text", []byte("hello world\n"), false},
		{"empty", []byte{}, false},
		{"null byte", []byte("hello\x00world"), true},
		{"png header", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00}, true},
		{"utf8", []byte("caf\xc3\xa9"), false},
	}
	for _, tt := range tests {
		if got := isBinary(tt.data); got != tt.binary {
			t.Errorf("isBinary(%s) = %v, want %v", tt.name, got, tt.binary)
		}
	}
}
