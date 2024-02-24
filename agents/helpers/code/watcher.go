package code

import (
	"context"
	"os"
	"path"
	"path/filepath"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/wool"
	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	events  chan<- Change
	watcher *fsnotify.Watcher

	// internal
	base  string
	pause bool
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
		if !shared.FileExists(fullPath) && !shared.DirectoryExists(fullPath) {
			w.Trace("skipping", wool.Field("path", fullPath))
			continue
		}
		err := filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			// If path doesn't exist we skip
			if info.IsDir() {
				return nil
			}
			if !dep.Keep(path) {
				w.Trace("skipping", wool.Field("path", path))
				return nil
			}
			err = watcher.Add(path)
			if err != nil {
				return w.Wrapf(err, "cannot add path: %s", path)
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
	watcher := &Watcher{watcher: fswatcher, base: base, events: events}

	watcherContext := context.Background()
	watcherContext = w.Inject(watcherContext)
	go watcher.Start(watcherContext)

	return watcher, nil
}

func (watcher *Watcher) Start(ctx context.Context) {
	w := wool.Get(ctx).In("Watcher.Start")
	// Start listening for events.
	defer watcher.watcher.Close()
	for {
		select {
		case event, ok := <-watcher.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) {
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
