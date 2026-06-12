package code

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/wool"
	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	events  chan<- Change
	watcher *fsnotify.Watcher

	// internal
	base         string
	dependencies *builders.Dependencies
	pause        bool
}

type Change struct {
	Path       string
	IsRelative bool
}

func addDependency(ctx context.Context, base string, dep *builders.Dependency, watcher *fsnotify.Watcher) error {
	w := wool.Get(ctx).In("addDependency")
	for _, p := range dep.Components() {
		fullPath := path.Join(base, p)
		// If path doesn't exist we skip
		if exists, err := shared.Exists(ctx, fullPath); err != nil || !exists {
			w.Trace("skipping", wool.Path(fullPath))
			continue
		}
		err := filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			// Watch DIRECTORIES, not individual files. fsnotify file-watches
			// die on atomic-rename saves — most editors write a temp file then
			// rename it over the original, which destroys the watched inode, so
			// the Write event is lost and hot-reload silently stops working
			// (this is exactly why pkg/** edits didn't reload). A directory
			// watch survives that and fires Create/Write/Rename for files
			// within; the event loop filters by the dependency select.
			if info.IsDir() {
				base := filepath.Base(path)
				if base == ".next" || base == "node_modules" || base == "__pycache__" || base == ".git" {
					return filepath.SkipDir
				}
				w.Trace("watching dir", wool.Path(path))
				if err := watcher.Add(path); err != nil {
					return w.Wrapf(err, "cannot add dir: %s", path)
				}
				return nil
			}
			// A single-file dependency (e.g. service.codefly.yaml) is never
			// visited as a dir by Walk — watch its parent directory instead.
			if dep.Keep(path) {
				if err := watcher.Add(filepath.Dir(path)); err != nil {
					return w.Wrapf(err, "cannot add file dir: %s", path)
				}
			}
			return nil
		})
		if err != nil {
			return w.Wrapf(err, "cannot walk path: %s", fullPath)
		}
	}
	return nil
}

func NewWatcher(ctx context.Context, events chan<- Change, base string, dependencies *builders.Dependencies) (*Watcher, error) {
	w := wool.Get(ctx).In("NewWatcher")
	// Add new watcher.
	fswatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, w.Wrapf(err, "cannot create fsnotify watcher")
	}

	for _, dep := range dependencies.Components {
		err = addDependency(ctx, base, dep, fswatcher)
		if err != nil {
			return nil, w.Wrapf(err, "cannot add dependency")
		}
	}
	watcher := &Watcher{watcher: fswatcher, base: base, events: events, dependencies: dependencies}

	return watcher, nil
}

// keep reports whether a changed path matches any watched dependency select.
// We watch whole directories now, so this filters out non-source churn
// (temp files, build output) that a directory watch also surfaces.
// isTransientFile reports whether a path is an editor temp / backup / lock
// file that fires filesystem events during a save but is not a real source
// change: generic temp/backup suffixes (.tmp/.bak/~), vim swap files
// (.swp/.swx/.swo/.swpx), and emacs lock/autosave (.#name, #name#). The
// atomic-rename TARGET (the real file) is not transient and still triggers a
// rebuild — so saves still hot-reload, but the temp churn around them doesn't.
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

func (watcher *Watcher) keep(absPath string) bool {
	if watcher.dependencies == nil {
		return true
	}
	for _, dep := range watcher.dependencies.Components {
		if dep.Keep(absPath) {
			return true
		}
	}
	return false
}

func (watcher *Watcher) Start(ctx context.Context) {
	w := wool.Get(ctx).In("Watcher.Start")
	// Start listening for events.
	defer watcher.watcher.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.watcher.Events:
			if !ok {
				return
			}
			// Write covers in-place saves; Create + Rename cover atomic-rename
			// saves (write temp → rename over original), which is how most
			// editors save — and which the old Write-only loop dropped.
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
				// A newly-created directory (e.g. a new package) must be added
				// to the watch set — fsnotify is not recursive.
				if event.Has(fsnotify.Create) {
					if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
						_ = watcher.watcher.Add(event.Name)
						continue
					}
				}
				// Editor temp/backup files fire Create/Rename during atomic
				// saves (write temp → rename over the original) but are not real
				// source changes. A named-file dependency with no path select
				// (e.g. service.codefly.yaml) matches EVERY path, so without this
				// skip a `foo.go.tmp` triggers a spurious rebuild — and rebuild
				// churn races the restart and wedges hot-reload. The rename
				// TARGET (the real file) is not transient and still triggers.
				if isTransientFile(event.Name) {
					continue
				}
				// We watch directories, so drop events for paths that don't
				// match a watched dependency (build output, unrelated files).
				if !watcher.keep(event.Name) {
					continue
				}
				w.Debug("got event", wool.Field("event", event))
				rel, err := filepath.Rel(watcher.base, event.Name)
				if err != nil {
					w.Error("cannot get relative path", wool.Field("base", watcher.base), wool.Field("path", event.Name))
					continue
				}
				change := Change{
					Path:       rel,
					IsRelative: true,
				}
				watcher.Handle(change)

				continue
			}
		case _, ok := <-watcher.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func (watcher *Watcher) Handle(event Change) {
	if watcher.pause {
		return
	}
	watcher.events <- event
}

func (watcher *Watcher) Pause() {
	watcher.pause = true
}

func (watcher *Watcher) Resume() {
	watcher.pause = false
}
