package code

import (
	"context"
	"path"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/wool"
	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	events  chan<- Change
	watcher *fsnotify.Watcher

	// internal
	excludes []string
	base     string
}

type Change struct {
	Path       string
	IsRelative bool
}

func NewWatcher(ctx context.Context, events chan<- Change, base string, includes []string, excludes ...string) (*Watcher, error) {
	w := wool.Get(ctx)
	// Add new watcher.
	fswatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, w.Wrapf(err, "cannot create fsnotify watcher")
	}
	for _, p := range includes {
		err = fswatcher.Add(path.Join(base, p))
		if err != nil {
			return nil, w.Wrapf(err, "cannot add path: %s", p)
		}
	}

	watcher := &Watcher{watcher: fswatcher, base: base, events: events}
	watcher.excludes = append(watcher.excludes, excludes...)
	go watcher.Start()
	return watcher, nil
}

func (w *Watcher) Start() {
	// Start listening for events.
	defer w.watcher.Close()
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			for _, exclude := range w.excludes {
				if strings.Contains(event.Name, exclude) {
					continue
				}
			}

			if event.Has(fsnotify.Write) {
				rel, err := filepath.Rel(w.base, event.Name)
				if err == nil {
					w.events <- Change{
						Path:       rel,
						IsRelative: true,
					}
					continue
				}
				w.events <- Change{
					Path: event.Name,
				}
			}
		case _, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}
