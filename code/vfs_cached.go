package code

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// CachedVFS wraps a base VFS (typically LocalVFS) with an in-memory file tree
// cache. Metadata operations (Stat, ReadDir, WalkDir) are served from cache.
// Reads/writes pass through to the base and update the cache.
//
// On startup, the entire source directory is walked to build the cache.
// A fsnotify watcher keeps the cache fresh on file changes.
type CachedVFS struct {
	base         VFS
	root         string
	mu           sync.RWMutex
	entries      map[string]*cachedEntry // absolute path → entry
	watcher      *fsnotify.Watcher
	stopCh       chan struct{}
	contentCache *ByteLRU       // optional: caches file content in RAM (nil = disabled)
	trigramIdx   *TrigramIndex  // optional: trigram index for fast search (nil = disabled)
}

type cachedEntry struct {
	name    string
	size    int64
	modTime time.Time
	isDir   bool
}

// skipDirs are directories never entered during the initial walk or watches.
var skipDirs = map[string]bool{
	".git": true, ".jj": true, "vendor": true, "node_modules": true,
	"__pycache__": true, "dist": true, "build": true,
	"target": true, ".cache": true, ".idea": true, ".vscode": true,
}

// NewCachedVFS creates a CachedVFS rooted at dir, backed by base.
// It walks dir to populate the cache and starts a background fsnotify watcher.
func NewCachedVFS(base VFS, dir string) (*CachedVFS, error) {
	c := &CachedVFS{
		base:    base,
		root:    filepath.Clean(dir),
		entries: make(map[string]*cachedEntry),
		stopCh:  make(chan struct{}),
	}

	// Initial population
	if err := c.populate(); err != nil {
		return nil, err
	}

	// Start watcher (best-effort — don't fail if unavailable)
	if w, err := fsnotify.NewWatcher(); err == nil {
		c.watcher = w
		c.addWatchDirs()
		go c.watchLoop()
	}

	return c, nil
}

// populate walks the root directory and fills the entry cache.
func (c *CachedVFS) populate() error {
	return c.base.WalkDir(c.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && skipDirs[d.Name()] && path != c.root {
			return fs.SkipDir
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		abs := filepath.Clean(path)
		c.entries[abs] = &cachedEntry{
			name:    d.Name(),
			size:    info.Size(),
			modTime: info.ModTime(),
			isDir:   d.IsDir(),
		}
		return nil
	})
}

// addWatchDirs adds all cached directories to the fsnotify watcher.
func (c *CachedVFS) addWatchDirs() {
	for path, entry := range c.entries {
		if entry.isDir {
			_ = c.watcher.Add(path)
		}
	}
}

// watchLoop processes fsnotify events and updates the cache.
func (c *CachedVFS) watchLoop() {
	// Debounce: collect events for 50ms then process batch
	timer := time.NewTimer(50 * time.Millisecond)
	timer.Stop()
	var pending []fsnotify.Event

	for {
		select {
		case <-c.stopCh:
			timer.Stop()
			return
		case ev, ok := <-c.watcher.Events:
			if !ok {
				return
			}
			pending = append(pending, ev)
			timer.Reset(50 * time.Millisecond)
		case <-timer.C:
			c.processBatch(pending)
			pending = pending[:0]
		case _, ok := <-c.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func (c *CachedVFS) processBatch(events []fsnotify.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, ev := range events {
		abs := filepath.Clean(ev.Name)

		if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
			// Remove entry and all children (if directory)
			if entry, ok := c.entries[abs]; ok && entry.isDir {
				prefix := abs + string(filepath.Separator)
				for p := range c.entries {
					if strings.HasPrefix(p, prefix) {
						delete(c.entries, p)
						if c.contentCache != nil {
							c.contentCache.Invalidate(p)
						}
					}
				}
				if c.watcher != nil {
					_ = c.watcher.Remove(abs)
				}
			}
			delete(c.entries, abs)
			if c.contentCache != nil {
				c.contentCache.Invalidate(abs)
			}
			if c.trigramIdx != nil {
				rel, _ := filepath.Rel(c.root, abs)
				c.trigramIdx.RemoveFile(rel)
			}
			continue
		}

		if ev.Has(fsnotify.Create) || ev.Has(fsnotify.Write) || ev.Has(fsnotify.Chmod) {
			info, err := c.base.Stat(abs)
			if err != nil {
				delete(c.entries, abs)
				continue
			}
			c.entries[abs] = &cachedEntry{
				name:    info.Name(),
				size:    info.Size(),
				modTime: info.ModTime(),
				isDir:   info.IsDir(),
			}
			if info.IsDir() && !skipDirs[info.Name()] && c.watcher != nil {
				_ = c.watcher.Add(abs)
			}
			if !info.IsDir() {
				// Invalidate content cache on file change (lazy reload on next read).
				if c.contentCache != nil {
					c.contentCache.Invalidate(abs)
				}
				// Update trigram index with new content.
				if c.trigramIdx != nil {
					rel, _ := filepath.Rel(c.root, abs)
					if data, err := c.base.ReadFile(abs); err == nil {
						c.trigramIdx.AddFile(rel, data)
					}
				}
			}
		}
	}
}

// Close stops the watcher and releases resources.
func (c *CachedVFS) Close() error {
	close(c.stopCh)
	if c.watcher != nil {
		return c.watcher.Close()
	}
	return nil
}

// Invalidate forces a full re-scan of the tree. Use sparingly.
func (c *CachedVFS) Invalidate() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cachedEntry)
	if c.contentCache != nil {
		c.contentCache.Clear()
	}
	return c.populate()
}

// --- VFS interface ---

func (c *CachedVFS) ReadFile(path string) ([]byte, error) {
	if c.contentCache != nil {
		abs := filepath.Clean(path)
		if data := c.contentCache.Get(abs); data != nil {
			return data, nil
		}
		data, err := c.base.ReadFile(path)
		if err != nil {
			return nil, err
		}
		c.contentCache.Put(abs, data)
		return data, nil
	}
	return c.base.ReadFile(path)
}

func (c *CachedVFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	err := c.base.WriteFile(path, data, perm)
	if err != nil {
		return err
	}
	// Update cache immediately (don't wait for fsnotify)
	abs := filepath.Clean(path)
	c.mu.Lock()
	c.entries[abs] = &cachedEntry{
		name:    filepath.Base(abs),
		size:    int64(len(data)),
		modTime: time.Now(),
		isDir:   false,
	}
	c.mu.Unlock()
	if c.contentCache != nil {
		c.contentCache.Put(abs, data)
	}
	return nil
}

func (c *CachedVFS) Remove(path string) error {
	err := c.base.Remove(path)
	if err != nil {
		return err
	}
	abs := filepath.Clean(path)
	c.mu.Lock()
	delete(c.entries, abs)
	c.mu.Unlock()
	if c.contentCache != nil {
		c.contentCache.Invalidate(abs)
	}
	return nil
}

func (c *CachedVFS) Rename(oldpath, newpath string) error {
	err := c.base.Rename(oldpath, newpath)
	if err != nil {
		return err
	}
	oldAbs := filepath.Clean(oldpath)
	newAbs := filepath.Clean(newpath)
	c.mu.Lock()
	if entry, ok := c.entries[oldAbs]; ok {
		entry.name = filepath.Base(newAbs)
		c.entries[newAbs] = entry
		delete(c.entries, oldAbs)
	}
	c.mu.Unlock()
	return nil
}

func (c *CachedVFS) Stat(path string) (os.FileInfo, error) {
	abs := filepath.Clean(path)
	c.mu.RLock()
	entry, ok := c.entries[abs]
	c.mu.RUnlock()
	if ok {
		return &memFileInfo{name: entry.name, size: entry.size, dir: entry.isDir, modTime: entry.modTime}, nil
	}
	// Cache miss — stat from disk and cache
	info, err := c.base.Stat(path)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.entries[abs] = &cachedEntry{
		name:    info.Name(),
		size:    info.Size(),
		modTime: info.ModTime(),
		isDir:   info.IsDir(),
	}
	c.mu.Unlock()
	return info, nil
}

func (c *CachedVFS) MkdirAll(path string, perm os.FileMode) error {
	err := c.base.MkdirAll(path, perm)
	if err != nil {
		return err
	}
	abs := filepath.Clean(path)
	c.mu.Lock()
	for d := abs; d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, ok := c.entries[d]; ok {
			break
		}
		c.entries[d] = &cachedEntry{
			name:    filepath.Base(d),
			isDir:   true,
			modTime: time.Now(),
		}
	}
	c.mu.Unlock()
	return nil
}

func (c *CachedVFS) WalkDir(root string, fn fs.WalkDirFunc) error {
	rootAbs := filepath.Clean(root)

	c.mu.RLock()
	// Collect all paths under root from cache, sorted
	var paths []string
	for p := range c.entries {
		if p == rootAbs || strings.HasPrefix(p, rootAbs+string(filepath.Separator)) {
			paths = append(paths, p)
		}
	}
	// Build snapshot to avoid holding lock during callback
	type walkEntry struct {
		path  string
		entry *cachedEntry
	}
	sort.Strings(paths)
	snapshot := make([]walkEntry, len(paths))
	for i, p := range paths {
		snapshot[i] = walkEntry{path: p, entry: c.entries[p]}
	}
	c.mu.RUnlock()

	var skipPrefixes []string
	for _, we := range snapshot {
		skip := false
		for _, sp := range skipPrefixes {
			if strings.HasPrefix(we.path, sp) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		de := &cachedDirEntry{entry: we.entry}
		if err := fn(we.path, de, nil); err != nil {
			if err == fs.SkipDir {
				skipPrefixes = append(skipPrefixes, we.path+string(filepath.Separator))
				continue
			}
			return err
		}
	}
	return nil
}

func (c *CachedVFS) ReadDir(path string) ([]os.DirEntry, error) {
	abs := filepath.Clean(path)
	prefix := abs + string(filepath.Separator)

	c.mu.RLock()
	defer c.mu.RUnlock()

	seen := make(map[string]bool)
	var entries []os.DirEntry

	for p, entry := range c.entries {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		// Only direct children (no deeper nesting)
		rest := strings.TrimPrefix(p, prefix)
		if strings.Contains(rest, string(filepath.Separator)) {
			continue
		}
		if seen[entry.name] {
			continue
		}
		seen[entry.name] = true
		entries = append(entries, &cachedDirEntry{entry: entry})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// --- cachedDirEntry implements fs.DirEntry ---

type cachedDirEntry struct {
	entry *cachedEntry
}

func (d *cachedDirEntry) Name() string               { return d.entry.name }
func (d *cachedDirEntry) IsDir() bool                 { return d.entry.isDir }
func (d *cachedDirEntry) Type() fs.FileMode           { if d.entry.isDir { return fs.ModeDir }; return 0 }
func (d *cachedDirEntry) Info() (fs.FileInfo, error)   {
	return &memFileInfo{name: d.entry.name, size: d.entry.size, dir: d.entry.isDir, modTime: d.entry.modTime}, nil
}
