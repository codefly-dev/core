package code

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// vfsFactory creates a VFS rooted at root, pre-seeded with files.
type vfsFactory struct {
	name    string
	create  func(t *testing.T, root string, seed map[string]string) VFS
	cleanup func()
}

func allVFSFactories(t *testing.T) []vfsFactory {
	t.Helper()
	return []vfsFactory{
		{
			name: "LocalVFS",
			create: func(t *testing.T, root string, seed map[string]string) VFS {
				t.Helper()
				for relPath, content := range seed {
					absPath := filepath.Join(root, relPath)
					os.MkdirAll(filepath.Dir(absPath), 0o755)
					os.WriteFile(absPath, []byte(content), 0o644)
				}
				return LocalVFS{}
			},
		},
		{
			name: "MemoryVFS",
			create: func(t *testing.T, root string, seed map[string]string) VFS {
				t.Helper()
				absFiles := make(map[string]string, len(seed))
				for relPath, content := range seed {
					absFiles[filepath.Join(root, relPath)] = content
				}
				return NewMemoryVFSFrom(absFiles)
			},
		},
		{
			name: "OverlayVFS_on_Memory",
			create: func(t *testing.T, root string, seed map[string]string) VFS {
				t.Helper()
				absFiles := make(map[string]string, len(seed))
				for relPath, content := range seed {
					absFiles[filepath.Join(root, relPath)] = content
				}
				base := NewMemoryVFSFrom(absFiles)
				return NewOverlayVFS(base)
			},
		},
	}
}

func seedFiles() map[string]string {
	return map[string]string{
		"hello.txt":       "hello world\n",
		"sub/nested.go":   "package sub\n",
		"sub/deep/a.txt":  "aaa",
		"other/readme.md": "# readme\n",
	}
}

func TestVFS_ReadFile(t *testing.T) {
	for _, fac := range allVFSFactories(t) {
		t.Run(fac.name, func(t *testing.T) {
			root := t.TempDir()
			vfs := fac.create(t, root, seedFiles())

			data, err := vfs.ReadFile(filepath.Join(root, "hello.txt"))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if string(data) != "hello world\n" {
				t.Errorf("content = %q", data)
			}

			_, err = vfs.ReadFile(filepath.Join(root, "nonexistent.txt"))
			if !os.IsNotExist(err) {
				t.Errorf("expected not-exist error, got %v", err)
			}
		})
	}
}

func TestVFS_WriteFile_ReadFile(t *testing.T) {
	for _, fac := range allVFSFactories(t) {
		t.Run(fac.name, func(t *testing.T) {
			root := t.TempDir()
			vfs := fac.create(t, root, seedFiles())

			path := filepath.Join(root, "new_file.txt")
			if err := vfs.WriteFile(path, []byte("new content"), 0o644); err != nil {
				t.Fatal(err)
			}
			data, err := vfs.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "new content" {
				t.Errorf("content = %q", data)
			}
		})
	}
}

func TestVFS_Remove(t *testing.T) {
	for _, fac := range allVFSFactories(t) {
		t.Run(fac.name, func(t *testing.T) {
			root := t.TempDir()
			vfs := fac.create(t, root, seedFiles())

			path := filepath.Join(root, "hello.txt")
			if err := vfs.Remove(path); err != nil {
				t.Fatal(err)
			}
			_, err := vfs.ReadFile(path)
			if !os.IsNotExist(err) {
				t.Errorf("expected not-exist after remove, got %v", err)
			}

			err = vfs.Remove(filepath.Join(root, "nonexistent.txt"))
			if !os.IsNotExist(err) {
				t.Errorf("expected not-exist on double remove, got %v", err)
			}
		})
	}
}

func TestVFS_Rename(t *testing.T) {
	for _, fac := range allVFSFactories(t) {
		t.Run(fac.name, func(t *testing.T) {
			root := t.TempDir()
			vfs := fac.create(t, root, seedFiles())

			old := filepath.Join(root, "hello.txt")
			new := filepath.Join(root, "moved.txt")
			if err := vfs.Rename(old, new); err != nil {
				t.Fatal(err)
			}
			data, err := vfs.ReadFile(new)
			if err != nil {
				t.Fatalf("ReadFile new: %v", err)
			}
			if string(data) != "hello world\n" {
				t.Errorf("content = %q", data)
			}
			_, err = vfs.ReadFile(old)
			if !os.IsNotExist(err) {
				t.Errorf("old file should be gone, got %v", err)
			}
		})
	}
}

func TestVFS_Stat(t *testing.T) {
	for _, fac := range allVFSFactories(t) {
		t.Run(fac.name, func(t *testing.T) {
			root := t.TempDir()
			vfs := fac.create(t, root, seedFiles())

			info, err := vfs.Stat(filepath.Join(root, "hello.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if info.IsDir() {
				t.Error("hello.txt should not be a directory")
			}
			if info.Size() != int64(len("hello world\n")) {
				t.Errorf("size = %d", info.Size())
			}

			info, err = vfs.Stat(filepath.Join(root, "sub"))
			if err != nil {
				t.Fatal(err)
			}
			if !info.IsDir() {
				t.Error("sub should be a directory")
			}

			_, err = vfs.Stat(filepath.Join(root, "nonexistent"))
			if !os.IsNotExist(err) {
				t.Errorf("expected not-exist, got %v", err)
			}
		})
	}
}

func TestVFS_MkdirAll(t *testing.T) {
	for _, fac := range allVFSFactories(t) {
		t.Run(fac.name, func(t *testing.T) {
			root := t.TempDir()
			vfs := fac.create(t, root, seedFiles())

			dir := filepath.Join(root, "a", "b", "c")
			if err := vfs.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			info, err := vfs.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !info.IsDir() {
				t.Error("should be a directory")
			}
		})
	}
}

func TestVFS_WalkDir(t *testing.T) {
	for _, fac := range allVFSFactories(t) {
		t.Run(fac.name, func(t *testing.T) {
			root := t.TempDir()
			vfs := fac.create(t, root, seedFiles())

			var paths []string
			err := vfs.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				rel, _ := filepath.Rel(root, path)
				if rel != "." {
					paths = append(paths, rel)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(paths)

			expected := []string{"hello.txt", "other", "other/readme.md", "sub", "sub/deep", "sub/deep/a.txt", "sub/nested.go"}
			sort.Strings(expected)

			if len(paths) != len(expected) {
				t.Fatalf("walk got %d paths %v, want %d paths %v", len(paths), paths, len(expected), expected)
			}
			for i := range expected {
				if paths[i] != expected[i] {
					t.Errorf("path[%d] = %q, want %q", i, paths[i], expected[i])
				}
			}
		})
	}
}

func TestVFS_WalkDir_SkipDir(t *testing.T) {
	for _, fac := range allVFSFactories(t) {
		t.Run(fac.name, func(t *testing.T) {
			root := t.TempDir()
			vfs := fac.create(t, root, seedFiles())

			var paths []string
			err := vfs.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() && d.Name() == "sub" {
					return fs.SkipDir
				}
				rel, _ := filepath.Rel(root, path)
				if rel != "." {
					paths = append(paths, rel)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range paths {
				if strings.HasPrefix(p, "sub") {
					t.Errorf("should have skipped sub, but got %q", p)
				}
			}
		})
	}
}

func TestVFS_ReadDir(t *testing.T) {
	for _, fac := range allVFSFactories(t) {
		t.Run(fac.name, func(t *testing.T) {
			root := t.TempDir()
			vfs := fac.create(t, root, seedFiles())

			entries, err := vfs.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			sort.Strings(names)
			expected := []string{"hello.txt", "other", "sub"}
			if len(names) != len(expected) {
				t.Fatalf("ReadDir got %v, want %v", names, expected)
			}
			for i := range expected {
				if names[i] != expected[i] {
					t.Errorf("names[%d] = %q, want %q", i, names[i], expected[i])
				}
			}
		})
	}
}

// --- OverlayVFS-specific tests ---

func TestOverlayVFS_ReadThrough(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/root/a.txt": "base content"})
	ov := NewOverlayVFS(base)

	data, err := ov.ReadFile("/root/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "base content" {
		t.Errorf("expected base content, got %q", data)
	}
}

func TestOverlayVFS_WriteOverrides(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/root/a.txt": "base"})
	ov := NewOverlayVFS(base)

	ov.WriteFile("/root/a.txt", []byte("overlay"), 0o644)
	data, _ := ov.ReadFile("/root/a.txt")
	if string(data) != "overlay" {
		t.Errorf("expected overlay content, got %q", data)
	}

	baseData, _ := base.ReadFile("/root/a.txt")
	if string(baseData) != "base" {
		t.Error("base should be untouched")
	}
}

func TestOverlayVFS_DeleteHidesBase(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/root/a.txt": "base"})
	ov := NewOverlayVFS(base)

	ov.Remove("/root/a.txt")
	_, err := ov.ReadFile("/root/a.txt")
	if !os.IsNotExist(err) {
		t.Errorf("expected not-exist after overlay delete, got %v", err)
	}

	baseData, _ := base.ReadFile("/root/a.txt")
	if string(baseData) != "base" {
		t.Error("base should still have the file")
	}
}

func TestOverlayVFS_Commit(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/root/a.txt": "base"})
	ov := NewOverlayVFS(base)

	ov.WriteFile("/root/a.txt", []byte("modified"), 0o644)
	ov.WriteFile("/root/b.txt", []byte("new file"), 0o644)

	if err := ov.Commit(); err != nil {
		t.Fatal(err)
	}

	data, _ := base.ReadFile("/root/a.txt")
	if string(data) != "modified" {
		t.Errorf("base a.txt = %q after commit", data)
	}
	data, _ = base.ReadFile("/root/b.txt")
	if string(data) != "new file" {
		t.Errorf("base b.txt = %q after commit", data)
	}
	if ov.Dirty() {
		t.Error("should not be dirty after commit")
	}
}

func TestOverlayVFS_Rollback(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/root/a.txt": "base"})
	ov := NewOverlayVFS(base)

	ov.WriteFile("/root/a.txt", []byte("modified"), 0o644)
	ov.Remove("/root/a.txt")
	ov.WriteFile("/root/new.txt", []byte("new"), 0o644)

	if !ov.Dirty() {
		t.Error("should be dirty before rollback")
	}

	ov.Rollback()

	if ov.Dirty() {
		t.Error("should not be dirty after rollback")
	}

	data, err := ov.ReadFile("/root/a.txt")
	if err != nil {
		t.Fatalf("a.txt should be readable after rollback: %v", err)
	}
	if string(data) != "base" {
		t.Errorf("a.txt = %q, want base", data)
	}
}

func TestOverlayVFS_Diff(t *testing.T) {
	base := NewMemoryVFSFrom(map[string]string{"/root/a.txt": "base", "/root/old.txt": "old"})
	ov := NewOverlayVFS(base)

	ov.WriteFile("/root/a.txt", []byte("modified"), 0o644)
	ov.WriteFile("/root/new.txt", []byte("new file"), 0o644)
	ov.Remove("/root/old.txt")

	changes := ov.Diff()
	if len(changes) != 3 {
		t.Fatalf("expected 3 changes, got %d: %+v", len(changes), changes)
	}

	byPath := make(map[string]FileChange)
	for _, c := range changes {
		byPath[c.Path] = c
	}

	if c := byPath["/root/a.txt"]; c.Type != "modify" {
		t.Errorf("a.txt type = %q, want modify", c.Type)
	}
	if c := byPath["/root/new.txt"]; c.Type != "create" {
		t.Errorf("new.txt type = %q, want create", c.Type)
	}
	if c := byPath["/root/old.txt"]; c.Type != "delete" {
		t.Errorf("old.txt type = %q, want delete", c.Type)
	}
}

func TestSearchVFS(t *testing.T) {
	root := "/project"
	m := NewMemoryVFSFrom(map[string]string{
		"/project/main.go":       "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n",
		"/project/util/helper.go": "package util\n\nfunc Helper() string {\n\treturn \"hello\"\n}\n",
	})

	result, err := SearchVFS(nil, m, root, SearchOpts{Pattern: "hello", Literal: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 2 {
		t.Fatalf("expected 2 matches, got %d: %+v", len(result.Matches), result.Matches)
	}

	result, err = SearchVFS(nil, m, root, SearchOpts{Pattern: "func.*Helper", CaseInsensitive: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 regex match, got %d", len(result.Matches))
	}
	if result.Matches[0].File != "util/helper.go" {
		t.Errorf("match file = %q", result.Matches[0].File)
	}
}
