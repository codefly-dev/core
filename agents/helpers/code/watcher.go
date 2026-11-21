package code

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codefly-dev/core/builders"
	"github.com/fsnotify/fsnotify"
)

// WatchTree is persistent directory interest, including a root which does not
// exist yet. Exclude receives absolute paths and must describe unconditional
// output exclusions, not ignore rules which may hide consumed inputs.
type WatchTree struct {
	Root    string
	Exclude func(string) bool
}

type watchRoot struct {
	legacy  bool // only legacy directory selection discards editor-like names
	path    string
	tree    bool
	keep    func(string) bool
	exclude func(string) bool
}

type Watcher struct {
	directories map[string]bool // event-loop-owned prior directory identities

	events   chan<- Change
	watcher  *fsnotify.Watcher
	base     string
	roots    []watchRoot
	paused   atomic.Bool
	statusMu sync.RWMutex
	watchErr error
}

type Change struct {
	Path       string
	IsRelative bool
	// Rescan requires owner input discovery even without a selected file event.
	Rescan bool
	// Err reports incomplete filesystem observation; it is not a source edit.
	Err error
}

// NewWatcher retains the existing dependency semantics. Explicit trees add
// recoverable directory interest and caller-owned traversal policy.
func NewWatcher(ctx context.Context, events chan<- Change, base string, dependencies *builders.Dependencies, trees ...WatchTree) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(base)
	if err != nil {
		_ = fw.Close()
		return nil, err
	}
	w := &Watcher{watcher: fw, events: events, base: absolute}
	if dependencies != nil {
		for _, dep := range dependencies.Components {
			for _, component := range dep.Components() {
				root := w.absolute(component)
				info, _ := os.Stat(root)
				w.roots = append(w.roots, watchRoot{path: root, tree: info != nil && info.IsDir(), keep: dep.Keep, exclude: legacyWatchExclusion, legacy: true})
			}
		}
	}
	for _, tree := range trees {
		w.roots = append(w.roots, watchRoot{path: w.absolute(tree.Root), tree: true, exclude: tree.Exclude})
	}
	if err = w.reconcile(ctx); err != nil {
		_ = fw.Close()
		return nil, err
	}
	return w, nil
}

func (w *Watcher) absolute(p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(w.base, p)
	}
	return filepath.Clean(p)
}

func withinWatch(root, p string) bool {
	relative, err := filepath.Rel(root, p)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func legacyWatchExclusion(p string) bool {
	switch filepath.Base(p) {
	case ".next", "node_modules", "__pycache__", ".git":
		return true
	}
	return false
}

// excluded applies directory policy to every ancestor, including deleted paths.
func (r watchRoot) excluded(p string) bool {
	if r.exclude == nil {
		return false
	}
	for withinWatch(r.path, p) {
		if r.exclude(p) {
			return true
		}
		if p == r.path {
			break
		}
		p = filepath.Dir(p)
	}
	return false
}

// reconcile keeps a nonrecursive parent sentinel for roots, then registers
// their full existing subtrees. Missing roots never broaden into a parent walk.
// It runs only on structural events or bounded recovery, not on every write.
func (w *Watcher) reconcile(ctx context.Context) error {
	desired := map[string]bool{}
	var failures []error
	add := func(p string) {
		desired[p] = true
		if err := w.watcher.Add(p); err != nil {
			failures = append(failures, fmt.Errorf("register watch %s: %w", p, err))
		}
	}
	for _, r := range w.roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent := filepath.Dir(r.path)
		for {
			info, err := os.Stat(parent)
			if err == nil && info.IsDir() {
				add(parent)
				break
			}
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				failures = append(failures, fmt.Errorf("watch ancestor %s: %w", parent, err))
				break
			}
			next := filepath.Dir(parent)
			if next == parent {
				failures = append(failures, fmt.Errorf("no directory ancestor for %s", r.path))
				break
			}
			parent = next
		}
		if !r.tree {
			continue
		}
		err := filepath.WalkDir(r.path, func(p string, entry fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if p == r.path {
					return fmt.Errorf("watch root %s is a symbolic link; select its resolved directory", p)
				}
				return nil
			}
			if entry.IsDir() {
				if r.excluded(p) {
					return filepath.SkipDir
				}
				add(p)
			}
			return nil
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("watch tree %s: %w", r.path, err))
		}
	}
	// Do not discard the previous successful coverage during a partial failure.
	if len(failures) == 0 {
		w.directories = desired
		for _, p := range w.watcher.WatchList() {
			if !desired[p] {
				_ = w.watcher.Remove(p)
			}
		}
	}
	return errors.Join(failures...)
}

// Err reports filesystem coverage only, not successful owner rediscovery or
// application readiness. Consumers must process Rescan before claiming either.
func (w *Watcher) Err() error         { w.statusMu.RLock(); defer w.statusMu.RUnlock(); return w.watchErr }
func (w *Watcher) setError(err error) { w.statusMu.Lock(); w.watchErr = err; w.statusMu.Unlock() }

func isTransientFile(p string) bool {
	base := filepath.Base(p)
	if strings.HasPrefix(base, ".#") || (strings.HasPrefix(base, "#") && strings.HasSuffix(base, "#")) {
		return true
	}
	for _, suf := range []string{".tmp", ".bak", ".swp", ".swx", ".swo", ".swpx", "~"} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return false
}

func (w *Watcher) keep(p string) bool {
	for _, r := range w.roots {
		if (p == r.path || r.tree && withinWatch(r.path, p)) && !r.excluded(p) && (!r.legacy || !r.tree || !isTransientFile(p)) && (r.keep == nil || r.keep(p)) {
			return true
		}
	}
	return false
}

func (w *Watcher) structural(p string) bool {
	for _, r := range w.roots {
		if r.legacy && r.tree && isTransientFile(p) {
			continue
		}
		// Ancestor sentinels only admit the path leading to a selected root.
		if withinWatch(p, r.path) {
			return true
		}
		if r.tree && withinWatch(r.path, p) && !r.excluded(p) {
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				return true
			}
			for watched := range w.directories {
				if withinWatch(p, watched) {
					return true
				}
			}
		}
	}
	return false
}

func (w *Watcher) Start(ctx context.Context) {
	defer w.watcher.Close()
	defer close(w.events) // sole closer, including cancellation while sending
	var retry *time.Timer
	var retryC <-chan time.Time
	delay := 100 * time.Millisecond
	defer func() {
		if retry != nil {
			retry.Stop()
		}
	}()
	recoverWatch := func(cause error) bool {
		w.setError(cause)
		if !w.send(ctx, Change{Err: cause, Rescan: true}) {
			return false
		}
		if retry == nil {
			retry = time.NewTimer(delay)
		} else {
			retry.Reset(delay)
		}
		retryC = retry.C
		if delay < 5*time.Second {
			delay *= 2
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
		}
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-retryC:
			retryC = nil
			if err := w.reconcile(ctx); err != nil {
				if !recoverWatch(err) {
					return
				}
				continue
			}
			w.setError(nil)
			delay = 100 * time.Millisecond
			if !w.send(ctx, Change{Rescan: true}) {
				return
			}
		case event, ok := <-w.watcher.Events:
			if !ok {
				failure := errors.New("filesystem event stream closed")
				w.setError(failure)
				w.send(ctx, Change{Err: failure, Rescan: true})
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			structural := w.structural(event.Name)
			selected := w.keep(event.Name)
			if structural {
				if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
					// A replacement can already occupy the same pathname. Drop
					// the old inode subscription before registering its successor.
					for _, watched := range w.watcher.WatchList() {
						if withinWatch(event.Name, watched) {
							_ = w.watcher.Remove(watched)
						}
					}
				}
				if err := w.reconcile(ctx); err != nil {
					if !recoverWatch(err) {
						return
					}
					continue
				}
			}
			if !selected && !structural {
				continue
			}
			relative, err := filepath.Rel(w.base, event.Name)
			if err != nil {
				if !recoverWatch(err) {
					return
				}
				continue
			}
			if !w.send(ctx, Change{Path: relative, IsRelative: true, Rescan: structural}) {
				return
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				failure := errors.New("filesystem error stream closed")
				w.setError(failure)
				w.send(ctx, Change{Err: failure, Rescan: true})
				return
			}
			if !recoverWatch(err) {
				return
			}
		}
	}
}

func (w *Watcher) send(ctx context.Context, event Change) bool {
	// Ordinary sibling traffic cannot stand in for restored full coverage.
	// A successful recovery emits one full Rescan after clearing watchErr.
	if event.Err == nil && (w.paused.Load() || w.Err() != nil) {
		return true
	}
	select {
	case w.events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func (w *Watcher) Handle(event Change) {
	if !w.paused.Load() {
		w.events <- event
	}
}
func (w *Watcher) Pause()  { w.paused.Store(true) }
func (w *Watcher) Resume() { w.paused.Store(false) }
