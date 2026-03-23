package code

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryVFS is a pure in-memory filesystem. All paths are absolute.
// Safe for concurrent use.
type MemoryVFS struct {
	mu    sync.RWMutex
	files map[string][]byte // absolute path -> content
	dirs  map[string]bool   // absolute path -> true (explicitly created dirs)
}

// NewMemoryVFS creates an empty in-memory filesystem.
func NewMemoryVFS() *MemoryVFS {
	return &MemoryVFS{
		files: make(map[string][]byte),
		dirs:  make(map[string]bool),
	}
}

// NewMemoryVFSFrom creates a MemoryVFS pre-populated with the given files.
// Keys are absolute paths, values are file contents.
func NewMemoryVFSFrom(files map[string]string) *MemoryVFS {
	m := NewMemoryVFS()
	for path, content := range files {
		m.files[filepath.Clean(path)] = []byte(content)
		m.ensureParents(filepath.Clean(path))
	}
	return m
}

func (m *MemoryVFS) ReadFile(path string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.files[filepath.Clean(path)]
	if !ok {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp, nil
}

func (m *MemoryVFS) WriteFile(path string, data []byte, _ os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := filepath.Clean(path)
	cp := make([]byte, len(data))
	copy(cp, data)
	m.files[p] = cp
	m.ensureParents(p)
	return nil
}

func (m *MemoryVFS) Remove(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := filepath.Clean(path)
	if _, ok := m.files[p]; ok {
		delete(m.files, p)
		return nil
	}
	if m.dirs[p] {
		delete(m.dirs, p)
		return nil
	}
	return &os.PathError{Op: "remove", Path: path, Err: os.ErrNotExist}
}

func (m *MemoryVFS) Rename(oldpath, newpath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	op := filepath.Clean(oldpath)
	np := filepath.Clean(newpath)
	data, ok := m.files[op]
	if !ok {
		return &os.PathError{Op: "rename", Path: oldpath, Err: os.ErrNotExist}
	}
	delete(m.files, op)
	m.files[np] = data
	m.ensureParents(np)
	return nil
}

func (m *MemoryVFS) Stat(path string) (os.FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := filepath.Clean(path)
	if data, ok := m.files[p]; ok {
		return &memFileInfo{name: filepath.Base(p), size: int64(len(data))}, nil
	}
	if m.dirs[p] || m.hasChildren(p) {
		return &memFileInfo{name: filepath.Base(p), dir: true}, nil
	}
	return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
}

func (m *MemoryVFS) MkdirAll(path string, _ os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := filepath.Clean(path)
	m.dirs[p] = true
	for p != "/" && p != "." {
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		m.dirs[parent] = true
		p = parent
	}
	return nil
}

func (m *MemoryVFS) WalkDir(root string, fn fs.WalkDirFunc) error {
	m.mu.RLock()
	entries := m.collectEntries(filepath.Clean(root))
	m.mu.RUnlock()

	sort.Strings(entries)
	var skipPrefixes []string
	for _, p := range entries {
		skip := false
		for _, sp := range skipPrefixes {
			if strings.HasPrefix(p, sp) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		m.mu.RLock()
		info := m.entryInfo(p)
		m.mu.RUnlock()
		if err := fn(p, info, nil); err != nil {
			if err == fs.SkipDir {
				skipPrefixes = append(skipPrefixes, p+string(filepath.Separator))
				continue
			}
			return err
		}
	}
	return nil
}

func (m *MemoryVFS) ReadDir(path string) ([]os.DirEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := filepath.Clean(path)
	seen := make(map[string]bool)
	var entries []os.DirEntry

	for fpath, data := range m.files {
		rel, err := filepath.Rel(p, fpath)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		name := parts[0]
		if seen[name] {
			continue
		}
		seen[name] = true
		if len(parts) > 1 {
			entries = append(entries, &memDirEntry{name: name, dir: true})
		} else {
			entries = append(entries, &memDirEntry{name: name, size: int64(len(data))})
		}
	}
	for dpath := range m.dirs {
		rel, err := filepath.Rel(p, dpath)
		if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
			continue
		}
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		name := parts[0]
		if seen[name] {
			continue
		}
		seen[name] = true
		entries = append(entries, &memDirEntry{name: name, dir: true})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// --- internal helpers ---

func (m *MemoryVFS) ensureParents(path string) {
	for p := filepath.Dir(path); p != "/" && p != "."; p = filepath.Dir(p) {
		if m.dirs[p] {
			break
		}
		m.dirs[p] = true
	}
}

func (m *MemoryVFS) hasChildren(prefix string) bool {
	pfx := prefix + string(filepath.Separator)
	for p := range m.files {
		if strings.HasPrefix(p, pfx) {
			return true
		}
	}
	for p := range m.dirs {
		if strings.HasPrefix(p, pfx) && p != prefix {
			return true
		}
	}
	return false
}

func (m *MemoryVFS) collectEntries(root string) []string {
	seen := make(map[string]bool)
	var entries []string

	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		entries = append(entries, p)
	}

	add(root)
	for p := range m.files {
		if !strings.HasPrefix(p, root) {
			continue
		}
		add(p)
		for d := filepath.Dir(p); d != root && strings.HasPrefix(d, root); d = filepath.Dir(d) {
			add(d)
		}
	}
	for p := range m.dirs {
		if strings.HasPrefix(p, root) {
			add(p)
		}
	}
	return entries
}

func (m *MemoryVFS) entryInfo(path string) fs.DirEntry {
	if data, ok := m.files[path]; ok {
		return &memDirEntry{name: filepath.Base(path), size: int64(len(data))}
	}
	return &memDirEntry{name: filepath.Base(path), dir: true}
}

// --- synthetic os.FileInfo / fs.DirEntry ---

type memFileInfo struct {
	name string
	size int64
	dir  bool
}

func (fi *memFileInfo) Name() string      { return fi.name }
func (fi *memFileInfo) Size() int64       { return fi.size }
func (fi *memFileInfo) Mode() os.FileMode { if fi.dir { return os.ModeDir | 0o755 }; return 0o644 }
func (fi *memFileInfo) ModTime() time.Time { return time.Time{} }
func (fi *memFileInfo) IsDir() bool       { return fi.dir }
func (fi *memFileInfo) Sys() interface{}  { return nil }

type memDirEntry struct {
	name string
	size int64
	dir  bool
}

func (e *memDirEntry) Name() string               { return e.name }
func (e *memDirEntry) IsDir() bool                { return e.dir }
func (e *memDirEntry) Type() fs.FileMode          { if e.dir { return fs.ModeDir }; return 0 }
func (e *memDirEntry) Info() (fs.FileInfo, error) {
	return &memFileInfo{name: e.name, size: e.size, dir: e.dir}, nil
}
