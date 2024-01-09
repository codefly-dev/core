package code

import (
	"context"
	"os"
	"path"
	"path/filepath"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/wool"
	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	events  chan<- Change
	watcher *fsnotify.Watcher

	// internal
	base       string
	dependency *builders.Dependency
}

type Change struct {
	Path       string
	IsRelative bool
}

func NewWatcher(ctx context.Context, events chan<- Change, base string, dependency *builders.Dependency) (*Watcher, error) {
	w := wool.Get(ctx)
	// Add new watcher.
	fswatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, w.Wrapf(err, "cannot create fsnotify watcher")
	}
	for _, p := range dependency.Components {
		fullPath := path.Join(base, p)
		err = filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if dependency.Ignore != nil && dependency.Ignore.Skip(path) {
				w.Trace("skipping", wool.Field("path", path))
				return nil
			}
			w.Trace("watching", wool.Field("path", path))
			err = fswatcher.Add(path)
			if err != nil {
				return w.Wrapf(err, "cannot add path: %s", path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	watcher := &Watcher{watcher: fswatcher, base: base, events: events, dependency: dependency}

	watcherContext := context.Background()
	watcherContext = w.Inject(watcherContext)
	go watcher.Start(watcherContext)

	return watcher, nil
}

func (watcher *Watcher) Start(ctx context.Context) {
	w := wool.Get(ctx).In("Start")
	// Start listening for events.
	defer watcher.watcher.Close()
	for {
		select {
		case event, ok := <-watcher.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) {
				w.Trace("got event", wool.Field("event", event))
				rel, err := filepath.Rel(watcher.base, event.Name)
				if err != nil {
					w.Error("cannot get relative path", wool.Field("base", watcher.base), wool.Field("path", event.Name))
					continue
				}

				watcher.events <- Change{
					Path:       rel,
					IsRelative: true,
				}
				continue
			}
		case _, ok := <-watcher.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}
