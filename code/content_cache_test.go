package code

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContentCache_ServedFromRAM(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n"), 0644)

	srv := NewDefaultCodeServer(dir, WithContentCache(0)) // 0 = default 200MB
	defer srv.Close()

	// First read: from disk, cached.
	data1, err := srv.FS.ReadFile(filepath.Join(dir, "hello.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data1) != "package main\n" {
		t.Fatalf("first read = %q", data1)
	}

	// Verify content is in the LRU cache.
	cached := srv.cachedFS
	if cached == nil {
		t.Fatal("expected CachedVFS to be active")
	}
	if cached.contentCache == nil {
		t.Fatal("expected content cache to be active")
	}
	if cached.contentCache.Len() != 1 {
		t.Errorf("cache entries = %d, want 1", cached.contentCache.Len())
	}

	// Second read: should come from cache (we can verify by checking cache size didn't change).
	data2, err := srv.FS.ReadFile(filepath.Join(dir, "hello.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data2) != "package main\n" {
		t.Fatalf("second read = %q", data2)
	}
}

func TestContentCache_InvalidateOnWrite(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n"), 0644)

	srv := NewDefaultCodeServer(dir, WithContentCache(0))
	defer srv.Close()

	// Prime cache.
	srv.FS.ReadFile(filepath.Join(dir, "hello.go"))

	// Write new content — should update cache.
	srv.FS.WriteFile(filepath.Join(dir, "hello.go"), []byte("package updated\n"), 0644)

	// Read should return updated content.
	data, err := srv.FS.ReadFile(filepath.Join(dir, "hello.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package updated\n" {
		t.Errorf("after write, read = %q, want 'package updated\\n'", data)
	}
}

func TestContentCache_InvalidateOnRemove(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n"), 0644)

	srv := NewDefaultCodeServer(dir, WithContentCache(0))
	defer srv.Close()

	// Prime cache.
	srv.FS.ReadFile(filepath.Join(dir, "hello.go"))
	if srv.cachedFS.contentCache.Len() != 1 {
		t.Fatal("expected 1 cached entry")
	}

	// Remove file.
	srv.FS.Remove(filepath.Join(dir, "hello.go"))

	if srv.cachedFS.contentCache.Len() != 0 {
		t.Errorf("cache entries = %d after remove, want 0", srv.cachedFS.contentCache.Len())
	}
}

func TestContentCache_SkipsBinaryFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "image.png"), []byte{0x89, 0x50, 0x4E, 0x47, 0x00, 0x01, 0x02}, 0644)

	srv := NewDefaultCodeServer(dir, WithContentCache(0))
	defer srv.Close()

	// Read binary file — should NOT be cached.
	srv.FS.ReadFile(filepath.Join(dir, "image.png"))

	if srv.cachedFS.contentCache.Len() != 0 {
		t.Error("binary file should not be cached")
	}
}

func TestContentCache_LRUEviction(t *testing.T) {
	dir := t.TempDir()
	// Create two 60-byte files.
	os.WriteFile(filepath.Join(dir, "a.go"), make([]byte, 60), 0644)
	os.WriteFile(filepath.Join(dir, "b.go"), make([]byte, 60), 0644)

	// Cache with only 100 bytes capacity — can hold one file, not both.
	// But these are all-zero bytes which look binary. Use text instead.
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(makeText(60)), 0644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte(makeText(60)), 0644)

	srv := NewDefaultCodeServer(dir, WithContentCache(100))
	defer srv.Close()

	srv.FS.ReadFile(filepath.Join(dir, "a.go"))
	srv.FS.ReadFile(filepath.Join(dir, "b.go"))

	// One should have been evicted.
	if srv.cachedFS.contentCache.Len() != 1 {
		t.Errorf("cache entries = %d, want 1 (LRU eviction)", srv.cachedFS.contentCache.Len())
	}
}

func TestContentCache_DefaultBudget(t *testing.T) {
	dir := t.TempDir()
	srv := NewDefaultCodeServer(dir, WithContentCache(0))
	defer srv.Close()

	if srv.cachedFS == nil || srv.cachedFS.contentCache == nil {
		t.Fatal("WithContentCache(0) should enable cache with default budget")
	}
	if srv.cachedFS.contentCache.capacity != 200*1024*1024 {
		t.Errorf("default budget = %d, want 200MB", srv.cachedFS.contentCache.capacity)
	}
}

// makeText creates n bytes of ASCII text (no null bytes).
func makeText(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a' + byte(i%26)
	}
	return string(b)
}
