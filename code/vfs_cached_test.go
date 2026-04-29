package code

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCachedVFS_BasicOperations(t *testing.T) {
	dir := t.TempDir()

	// Create initial files
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Hello"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "util.go"), []byte("package main"), 0o644)

	cached, err := NewCachedVFS(LocalVFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cached.Close()

	// Stat from cache (no disk hit)
	info, err := cached.Stat(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal("stat README.md:", err)
	}
	if info.Name() != "README.md" {
		t.Fatalf("expected README.md, got %s", info.Name())
	}
	if info.Size() != 7 {
		t.Fatalf("expected size 7, got %d", info.Size())
	}

	// ReadDir from cache
	entries, err := cached.ReadDir(filepath.Join(dir, "src"))
	if err != nil {
		t.Fatal("readdir src:", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries in src/, got %d", len(entries))
	}

	// ReadFile still goes to disk
	data, err := cached.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal("readfile:", err)
	}
	if string(data) != "# Hello" {
		t.Fatalf("expected '# Hello', got %q", string(data))
	}
}

func TestCachedVFS_WalkDir(t *testing.T) {
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(dir, "a", "one.txt"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(dir, "a", "b", "two.txt"), []byte("2"), 0o644)
	os.WriteFile(filepath.Join(dir, "root.txt"), []byte("r"), 0o644)

	cached, err := NewCachedVFS(LocalVFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cached.Close()

	var paths []string
	err = cached.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatal("walkdir:", err)
	}

	// Should find: ".", "a", "a/b", "a/b/two.txt", "a/one.txt", "root.txt"
	if len(paths) < 4 {
		t.Fatalf("expected >= 4 paths, got %d: %v", len(paths), paths)
	}
}

func TestCachedVFS_WriteUpdatesCache(t *testing.T) {
	dir := t.TempDir()

	cached, err := NewCachedVFS(LocalVFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cached.Close()

	// Write a new file
	newFile := filepath.Join(dir, "new.txt")
	err = cached.WriteFile(newFile, []byte("hello world"), 0o644)
	if err != nil {
		t.Fatal("write:", err)
	}

	// Cache should be updated immediately
	info, err := cached.Stat(newFile)
	if err != nil {
		t.Fatal("stat after write:", err)
	}
	if info.Size() != 11 {
		t.Fatalf("expected size 11, got %d", info.Size())
	}
}

func TestCachedVFS_RemoveUpdatesCache(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "gone.txt")
	os.WriteFile(f, []byte("bye"), 0o644)

	cached, err := NewCachedVFS(LocalVFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cached.Close()

	// File exists in cache
	_, err = cached.Stat(f)
	if err != nil {
		t.Fatal("stat before remove:", err)
	}

	// Remove it
	err = cached.Remove(f)
	if err != nil {
		t.Fatal("remove:", err)
	}

	// Cache should reflect removal
	_, err = cached.Stat(f)
	if err == nil {
		t.Fatal("expected stat error after remove")
	}
}

func TestCachedVFS_SkipDirs(t *testing.T) {
	dir := t.TempDir()

	// Create .git and node_modules — should be skipped during walk
	os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o755)
	os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0o644)
	os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "index.js"), []byte("//"), 0o644)
	os.WriteFile(filepath.Join(dir, "src.go"), []byte("package main"), 0o644)

	cached, err := NewCachedVFS(LocalVFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cached.Close()

	// WalkDir should NOT visit .git or node_modules
	var walkedPaths []string
	cached.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		rel, _ := filepath.Rel(dir, path)
		walkedPaths = append(walkedPaths, rel)
		return nil
	})

	for _, p := range walkedPaths {
		if filepath.Base(p) == ".git" || filepath.Base(p) == "node_modules" {
			t.Fatalf("WalkDir should skip %s", p)
		}
	}

	// src.go should be in cache
	_, err = cached.Stat(filepath.Join(dir, "src.go"))
	if err != nil {
		t.Fatal("expected src.go in cache:", err)
	}
}

func TestCachedVFS_FsnotifyUpdatesCache(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "initial.txt"), []byte("init"), 0o644)

	cached, err := NewCachedVFS(LocalVFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cached.Close()

	// Write a file directly to disk (bypassing CachedVFS)
	newFile := filepath.Join(dir, "external.txt")
	os.WriteFile(newFile, []byte("from outside"), 0o644)

	// Wait for fsnotify + debounce
	time.Sleep(200 * time.Millisecond)

	// Cache should pick it up
	info, err := cached.Stat(newFile)
	require.NoError(t, err, "fsnotify must work in this environment; if it doesn't, fix the runner")
	if info.Size() != 12 {
		t.Fatalf("expected size 12, got %d", info.Size())
	}
}
