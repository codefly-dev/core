package code

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FileChange records a single file mutation in the overlay.
type FileChange struct {
	Path    string
	Type    string // "create", "modify", "delete"
	Content []byte // nil for deletes
}

// OverlayVFS layers in-memory writes/deletes on top of a base VFS.
// Reads check the overlay first, then fall through to the base.
// Writes never touch the base until Commit() is called.
type OverlayVFS struct {
	mu            sync.RWMutex
	base          VFS
	writes        map[string][]byte // created or modified files
	deletes       map[string]bool   // deleted files
	dirs          map[string]bool   // created directories
	baseSnapshots map[string][]byte // snapshot of base content at first read/write for stale detection
}

// NewOverlayVFS wraps an existing VFS with an in-memory overlay.
func NewOverlayVFS(base VFS) *OverlayVFS {
	return &OverlayVFS{
		base:          base,
		writes:        make(map[string][]byte),
		deletes:       make(map[string]bool),
		dirs:          make(map[string]bool),
		baseSnapshots: make(map[string][]byte),
	}
}

// Base returns the underlying VFS.
func (o *OverlayVFS) Base() VFS {
	return o.base
}

// BaseSnapshot returns the base content captured when the overlay first
// touched a file. Returns nil, false if no snapshot exists.
func (o *OverlayVFS) BaseSnapshot(path string) ([]byte, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	p := filepath.Clean(path)
	data, ok := o.baseSnapshots[p]
	return data, ok
}

// ModifiedFiles returns the set of file paths that have pending writes.
func (o *OverlayVFS) ModifiedFiles() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	files := make([]string, 0, len(o.writes))
	for p := range o.writes {
		files = append(files, p)
	}
	return files
}

// DeletedFiles returns the set of file paths that have pending deletes.
func (o *OverlayVFS) DeletedFiles() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	files := make([]string, 0, len(o.deletes))
	for p := range o.deletes {
		files = append(files, p)
	}
	return files
}

func (o *OverlayVFS) ReadFile(path string) ([]byte, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	p := filepath.Clean(path)
	if o.deletes[p] {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	if data, ok := o.writes[p]; ok {
		cp := make([]byte, len(data))
		copy(cp, data)
		return cp, nil
	}
	return o.base.ReadFile(path)
}

func (o *OverlayVFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	p := filepath.Clean(path)
	if _, snapped := o.baseSnapshots[p]; !snapped {
		if original, err := o.base.ReadFile(p); err == nil {
			snap := make([]byte, len(original))
			copy(snap, original)
			o.baseSnapshots[p] = snap
		}
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	o.writes[p] = cp
	delete(o.deletes, p)
	o.ensureParents(p)
	return nil
}

func (o *OverlayVFS) Remove(path string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	p := filepath.Clean(path)

	if _, inWrites := o.writes[p]; inWrites {
		delete(o.writes, p)
		o.deletes[p] = true
		return nil
	}
	if _, err := o.base.Stat(p); err != nil {
		if o.dirs[p] {
			delete(o.dirs, p)
			return nil
		}
		return &os.PathError{Op: "remove", Path: path, Err: os.ErrNotExist}
	}
	if _, snapped := o.baseSnapshots[p]; !snapped {
		if original, err := o.base.ReadFile(p); err == nil {
			snap := make([]byte, len(original))
			copy(snap, original)
			o.baseSnapshots[p] = snap
		}
	}
	o.deletes[p] = true
	return nil
}

func (o *OverlayVFS) Rename(oldpath, newpath string) error {
	data, err := o.ReadFile(oldpath)
	if err != nil {
		return err
	}
	if err := o.WriteFile(newpath, data, 0o644); err != nil {
		return err
	}
	return o.Remove(oldpath)
}

func (o *OverlayVFS) Stat(path string) (os.FileInfo, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	p := filepath.Clean(path)
	if o.deletes[p] {
		return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
	}
	if data, ok := o.writes[p]; ok {
		return &memFileInfo{name: filepath.Base(p), size: int64(len(data))}, nil
	}
	if o.dirs[p] {
		return &memFileInfo{name: filepath.Base(p), dir: true}, nil
	}
	return o.base.Stat(path)
}

func (o *OverlayVFS) MkdirAll(path string, perm os.FileMode) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	p := filepath.Clean(path)
	o.dirs[p] = true
	for d := filepath.Dir(p); d != "/" && d != "." && d != p; d = filepath.Dir(d) {
		o.dirs[d] = true
		if d == filepath.Dir(d) {
			break
		}
	}
	return nil
}

func (o *OverlayVFS) WalkDir(root string, fn fs.WalkDirFunc) error {
	o.mu.RLock()

	seen := make(map[string]bool)
	var allPaths []string

	collectFromBase := func() {
		_ = o.base.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			p := filepath.Clean(path)
			if o.deletes[p] {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !seen[p] {
				seen[p] = true
				allPaths = append(allPaths, p)
			}
			return nil
		})
	}
	collectFromBase()

	rClean := filepath.Clean(root)
	for p := range o.writes {
		if strings.HasPrefix(p, rClean) && !seen[p] {
			seen[p] = true
			allPaths = append(allPaths, p)
			for d := filepath.Dir(p); d != rClean && strings.HasPrefix(d, rClean); d = filepath.Dir(d) {
				if !seen[d] {
					seen[d] = true
					allPaths = append(allPaths, d)
				}
			}
		}
	}
	for p := range o.dirs {
		if strings.HasPrefix(p, rClean) && !seen[p] {
			seen[p] = true
			allPaths = append(allPaths, p)
		}
	}

	o.mu.RUnlock()

	sort.Strings(allPaths)
	var skipPrefixes []string
	for _, p := range allPaths {
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
		o.mu.RLock()
		entry := o.dirEntry(p)
		o.mu.RUnlock()
		if err := fn(p, entry, nil); err != nil {
			if err == fs.SkipDir {
				skipPrefixes = append(skipPrefixes, p+string(filepath.Separator))
				continue
			}
			return err
		}
	}
	return nil
}

func (o *OverlayVFS) ReadDir(path string) ([]os.DirEntry, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	p := filepath.Clean(path)

	baseEntries, _ := o.base.ReadDir(path)
	seen := make(map[string]bool)
	var entries []os.DirEntry
	for _, e := range baseEntries {
		full := filepath.Join(p, e.Name())
		if o.deletes[full] {
			continue
		}
		seen[e.Name()] = true
		if data, ok := o.writes[full]; ok {
			entries = append(entries, &memDirEntry{name: e.Name(), size: int64(len(data))})
		} else {
			entries = append(entries, e)
		}
	}

	for wp, data := range o.writes {
		rel, err := filepath.Rel(p, wp)
		if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
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

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// Commit flushes all overlay writes to the base VFS and applies deletes.
func (o *OverlayVFS) Commit() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	for p := range o.dirs {
		if err := o.base.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	for p, data := range o.writes {
		if err := o.base.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := o.base.WriteFile(p, data, 0o644); err != nil {
			return err
		}
	}
	for p := range o.deletes {
		_ = o.base.Remove(p)
	}

	o.writes = make(map[string][]byte)
	o.deletes = make(map[string]bool)
	o.dirs = make(map[string]bool)
	o.baseSnapshots = make(map[string][]byte)
	return nil
}

// Rollback discards all overlay changes.
func (o *OverlayVFS) Rollback() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.writes = make(map[string][]byte)
	o.deletes = make(map[string]bool)
	o.dirs = make(map[string]bool)
	o.baseSnapshots = make(map[string][]byte)
}

// Diff returns a list of all file changes in the overlay.
func (o *OverlayVFS) Diff() []FileChange {
	o.mu.RLock()
	defer o.mu.RUnlock()
	var changes []FileChange
	for p, data := range o.writes {
		changeType := "modify"
		if _, err := o.base.Stat(p); err != nil {
			changeType = "create"
		}
		changes = append(changes, FileChange{Path: p, Type: changeType, Content: data})
	}
	for p := range o.deletes {
		changes = append(changes, FileChange{Path: p, Type: "delete"})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

// Dirty returns true if the overlay has any pending changes.
func (o *OverlayVFS) Dirty() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return len(o.writes) > 0 || len(o.deletes) > 0
}

func (o *OverlayVFS) ensureParents(path string) {
	for d := filepath.Dir(path); d != "/" && d != "."; d = filepath.Dir(d) {
		if o.dirs[d] {
			break
		}
		o.dirs[d] = true
	}
}

func (o *OverlayVFS) dirEntry(path string) fs.DirEntry {
	if data, ok := o.writes[path]; ok {
		return &memDirEntry{name: filepath.Base(path), size: int64(len(data))}
	}
	if o.dirs[path] {
		return &memDirEntry{name: filepath.Base(path), dir: true}
	}
	info, err := o.base.Stat(path)
	if err != nil {
		return &memDirEntry{name: filepath.Base(path), dir: true}
	}
	return &memDirEntry{name: info.Name(), size: info.Size(), dir: info.IsDir()}
}
