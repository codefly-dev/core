package code

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WorkspaceChangeKind is metadata-only evidence that a Codefly-owned
// workspace may have changed. Consumers must reconcile through typed Gateway
// reads; watcher events are deliberately never authoritative source state.
type WorkspaceChangeKind string

const (
	WorkspaceChangeCreate   WorkspaceChangeKind = "create"
	WorkspaceChangeWrite    WorkspaceChangeKind = "write"
	WorkspaceChangeRemove   WorkspaceChangeKind = "remove"
	WorkspaceChangeMetadata WorkspaceChangeKind = "metadata"
	WorkspaceChangeRescan   WorkspaceChangeKind = "rescan"
)

type WorkspaceChange struct {
	Kind         WorkspaceChangeKind
	Path         string
	PreviousPath string
	Reason       string
}

type WorkspaceChangeEvent struct {
	SourceID   string
	Sequence   uint64
	ObservedAt time.Time
	Changes    []WorkspaceChange
}

type WorkspaceChangeCursor struct {
	SourceID string
	Sequence uint64
}

var (
	ErrWorkspaceChangeMonitorClosed = errors.New("workspace change monitor closed")
	ErrWorkspaceChangeStreamSlow    = errors.New("workspace change stream consumer fell behind")
)

type workspaceChangeMonitorConfig struct {
	sourceID         string
	now              func() time.Time
	debounce         time.Duration
	replayLimit      int
	subscriberBuffer int
}

type WorkspaceChangeMonitorOption func(*workspaceChangeMonitorConfig) error

func WithWorkspaceChangeSourceID(sourceID string) WorkspaceChangeMonitorOption {
	return func(config *workspaceChangeMonitorConfig) error {
		if strings.TrimSpace(sourceID) == "" {
			return errors.New("workspace change source id is required")
		}
		config.sourceID = strings.TrimSpace(sourceID)
		return nil
	}
}

func WithWorkspaceChangeClock(now func() time.Time) WorkspaceChangeMonitorOption {
	return func(config *workspaceChangeMonitorConfig) error {
		if now == nil {
			return errors.New("workspace change clock is required")
		}
		config.now = now
		return nil
	}
}

func WithWorkspaceChangeReplayLimit(limit int) WorkspaceChangeMonitorOption {
	return func(config *workspaceChangeMonitorConfig) error {
		if limit <= 0 {
			return errors.New("workspace change replay limit must be positive")
		}
		config.replayLimit = limit
		return nil
	}
}

func WithWorkspaceChangeSubscriberBuffer(size int) WorkspaceChangeMonitorOption {
	return func(config *workspaceChangeMonitorConfig) error {
		if size <= 0 {
			return errors.New("workspace change subscriber buffer must be positive")
		}
		config.subscriberBuffer = size
		return nil
	}
}

// WorkspaceChangeMonitor owns recursive fsnotify observation inside Codefly.
// It retains a bounded replay window, sequences deterministic coalesced event
// batches, and converts every uncertainty into an explicit rescan event.
type WorkspaceChangeMonitor struct {
	root    string
	watcher *fsnotify.Watcher
	config  workspaceChangeMonitorConfig

	mu          sync.Mutex
	sequence    uint64
	replay      []WorkspaceChangeEvent
	subscribers map[uint64]*workspaceChangeSubscriber
	nextID      uint64
	watchDirs   map[string]struct{}
	closed      bool

	stop      chan struct{}
	closeOnce sync.Once
	closeErr  error
	wait      sync.WaitGroup
}

type workspaceChangeSubscriber struct {
	id      uint64
	monitor *WorkspaceChangeMonitor
	ctx     context.Context
	events  chan WorkspaceChangeEvent
	mu      sync.Mutex
	err     error
	closed  bool
}

type WorkspaceChangeSubscription struct {
	subscriber *workspaceChangeSubscriber
}

func NewWorkspaceChangeMonitor(root string, options ...WorkspaceChangeMonitorOption) (*WorkspaceChangeMonitor, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("workspace change monitor root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("workspace change monitor root: %w", err)
	}
	info, err := LocalVFS{}.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("workspace change monitor stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("workspace change monitor root is not a directory")
	}
	sourceID, err := randomWorkspaceChangeSourceID()
	if err != nil {
		return nil, err
	}
	config := workspaceChangeMonitorConfig{
		sourceID: sourceID, now: time.Now, debounce: 50 * time.Millisecond,
		replayLimit: 4_096, subscriberBuffer: 256,
	}
	for _, option := range options {
		if option != nil {
			if err := option(&config); err != nil {
				return nil, err
			}
		}
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("workspace change monitor create watcher: %w", err)
	}
	monitor := &WorkspaceChangeMonitor{
		root: filepath.Clean(absolute), watcher: watcher, config: config,
		subscribers: make(map[uint64]*workspaceChangeSubscriber),
		watchDirs:   make(map[string]struct{}), stop: make(chan struct{}),
	}
	if err := monitor.addTree(monitor.root); err != nil {
		_ = watcher.Close()
		return nil, err
	}
	monitor.wait.Add(1)
	go monitor.run()
	return monitor, nil
}

func randomWorkspaceChangeSourceID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("workspace change monitor source id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func (monitor *WorkspaceChangeMonitor) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && skipDirs[entry.Name()] {
			return filepath.SkipDir
		}
		path = filepath.Clean(path)
		if err := monitor.watcher.Add(path); err != nil {
			return fmt.Errorf("workspace change monitor watch %s: %w", path, err)
		}
		monitor.watchDirs[path] = struct{}{}
		return nil
	})
}

func (monitor *WorkspaceChangeMonitor) run() {
	defer monitor.wait.Done()
	var timer *time.Timer
	var timerC <-chan time.Time
	pending := make(map[string]fsnotify.Op)
	flush := func() {
		if len(pending) == 0 {
			return
		}
		changes := monitor.compileChanges(pending)
		clear(pending)
		for _, change := range changes {
			// One durable cursor position represents one canonical mutation.
			// Keeping that invariant avoids ambiguous partial acknowledgement
			// when downstream journals persist source/sequence pairs.
			monitor.publish([]WorkspaceChange{change})
		}
	}
	for {
		select {
		case <-monitor.stop:
			if timer != nil {
				timer.Stop()
			}
			return
		case event, ok := <-monitor.watcher.Events:
			if !ok {
				return
			}
			path := filepath.Clean(event.Name)
			if !monitor.contains(path) || monitor.skipped(path) {
				continue
			}
			pending[path] |= event.Op
			if timer == nil {
				timer = time.NewTimer(monitor.config.debounce)
				timerC = timer.C
			}
		case <-timerC:
			flush()
			timer = nil
			timerC = nil
		case _, ok := <-monitor.watcher.Errors:
			if !ok {
				return
			}
			flush()
			monitor.publish([]WorkspaceChange{{Kind: WorkspaceChangeRescan, Reason: "watcher_error"}})
		}
	}
}

func (monitor *WorkspaceChangeMonitor) compileChanges(pending map[string]fsnotify.Op) []WorkspaceChange {
	paths := make([]string, 0, len(pending))
	for path := range pending {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	changes := make([]WorkspaceChange, 0, len(paths))
	for _, absolute := range paths {
		op := pending[absolute]
		relative, err := filepath.Rel(monitor.root, absolute)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		relative = filepath.ToSlash(relative)
		info, statErr := os.Stat(absolute)
		if statErr == nil && info.IsDir() {
			if skipDirs[info.Name()] || !op.Has(fsnotify.Create) {
				continue
			}
			if err := monitor.addTree(absolute); err != nil {
				changes = append(changes, WorkspaceChange{Kind: WorkspaceChangeRescan, Reason: "directory_watch_failed"})
			} else {
				changes = append(changes, WorkspaceChange{Kind: WorkspaceChangeRescan, Reason: "directory_created"})
			}
			continue
		}
		if monitor.removeWatchedTree(absolute) {
			changes = append(changes, WorkspaceChange{Kind: WorkspaceChangeRescan, Reason: "directory_removed"})
			continue
		}
		switch {
		case statErr == nil && op.Has(fsnotify.Create):
			changes = append(changes, WorkspaceChange{Kind: WorkspaceChangeCreate, Path: relative})
		case statErr == nil && op.Has(fsnotify.Write):
			changes = append(changes, WorkspaceChange{Kind: WorkspaceChangeWrite, Path: relative})
		case statErr == nil && op.Has(fsnotify.Chmod):
			changes = append(changes, WorkspaceChange{Kind: WorkspaceChangeMetadata, Path: relative})
		case op.Has(fsnotify.Remove) || op.Has(fsnotify.Rename):
			changes = append(changes, WorkspaceChange{Kind: WorkspaceChangeRemove, Path: relative})
		case statErr != nil:
			changes = append(changes, WorkspaceChange{Kind: WorkspaceChangeRemove, Path: relative})
		}
	}
	return canonicalWorkspaceChanges(changes)
}

func canonicalWorkspaceChanges(changes []WorkspaceChange) []WorkspaceChange {
	seen := make(map[WorkspaceChange]struct{}, len(changes))
	result := make([]WorkspaceChange, 0, len(changes))
	for _, change := range changes {
		if change.Kind == WorkspaceChangeRescan {
			return []WorkspaceChange{{Kind: WorkspaceChangeRescan, Reason: change.Reason}}
		}
		if _, exists := seen[change]; exists {
			continue
		}
		seen[change] = struct{}{}
		result = append(result, change)
	}
	return result
}

func (monitor *WorkspaceChangeMonitor) removeWatchedTree(path string) bool {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	prefix := filepath.Clean(path) + string(filepath.Separator)
	removed := false
	for directory := range monitor.watchDirs {
		if directory == path || strings.HasPrefix(directory, prefix) {
			delete(monitor.watchDirs, directory)
			_ = monitor.watcher.Remove(directory)
			removed = true
		}
	}
	return removed
}

func (monitor *WorkspaceChangeMonitor) contains(path string) bool {
	relative, err := filepath.Rel(monitor.root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (monitor *WorkspaceChangeMonitor) skipped(path string) bool {
	relative, err := filepath.Rel(monitor.root, path)
	if err != nil {
		return true
	}
	for _, component := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if skipDirs[component] {
			return true
		}
	}
	return false
}

func (monitor *WorkspaceChangeMonitor) publish(changes []WorkspaceChange) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	monitor.publishLocked(changes)
}

func (monitor *WorkspaceChangeMonitor) publishLocked(changes []WorkspaceChange) WorkspaceChangeEvent {
	if monitor.closed || len(changes) == 0 {
		return WorkspaceChangeEvent{}
	}
	monitor.sequence++
	event := WorkspaceChangeEvent{
		SourceID: monitor.config.sourceID, Sequence: monitor.sequence,
		ObservedAt: monitor.config.now().UTC(), Changes: append([]WorkspaceChange(nil), changes...),
	}
	monitor.replay = append(monitor.replay, event)
	if len(monitor.replay) > monitor.config.replayLimit {
		monitor.replay = append([]WorkspaceChangeEvent(nil), monitor.replay[len(monitor.replay)-monitor.config.replayLimit:]...)
	}
	for id, subscriber := range monitor.subscribers {
		select {
		case subscriber.events <- cloneWorkspaceChangeEvent(event):
		default:
			subscriber.failLocked(ErrWorkspaceChangeStreamSlow)
			delete(monitor.subscribers, id)
		}
	}
	return event
}

func cloneWorkspaceChangeEvent(event WorkspaceChangeEvent) WorkspaceChangeEvent {
	event.Changes = append([]WorkspaceChange(nil), event.Changes...)
	return event
}

func (monitor *WorkspaceChangeMonitor) Subscribe(ctx context.Context, cursor WorkspaceChangeCursor) (*WorkspaceChangeSubscription, error) {
	if ctx == nil {
		return nil, errors.New("workspace change subscription context is required")
	}
	if (cursor.SourceID == "") != (cursor.Sequence == 0) {
		return nil, errors.New("workspace change cursor requires both source id and positive sequence")
	}
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if monitor.closed {
		return nil, ErrWorkspaceChangeMonitorClosed
	}
	monitor.nextID++
	subscriber := &workspaceChangeSubscriber{
		id: monitor.nextID, monitor: monitor, ctx: ctx,
		events: make(chan WorkspaceChangeEvent, monitor.config.subscriberBuffer),
	}
	monitor.subscribers[subscriber.id] = subscriber
	if cursor.SourceID == "" {
		return &WorkspaceChangeSubscription{subscriber: subscriber}, nil
	}
	if cursor.SourceID != monitor.config.sourceID {
		monitor.publishLocked([]WorkspaceChange{{Kind: WorkspaceChangeRescan, Reason: "source_changed"}})
		return &WorkspaceChangeSubscription{subscriber: subscriber}, nil
	}
	if cursor.Sequence > monitor.sequence {
		monitor.publishLocked([]WorkspaceChange{{Kind: WorkspaceChangeRescan, Reason: "cursor_ahead"}})
		return &WorkspaceChangeSubscription{subscriber: subscriber}, nil
	}
	start := 0
	for start < len(monitor.replay) && monitor.replay[start].Sequence <= cursor.Sequence {
		start++
	}
	if len(monitor.replay) > 0 && cursor.Sequence+1 < monitor.replay[0].Sequence {
		monitor.publishLocked([]WorkspaceChange{{Kind: WorkspaceChangeRescan, Reason: "replay_window_exceeded"}})
		return &WorkspaceChangeSubscription{subscriber: subscriber}, nil
	}
	if len(monitor.replay)-start > cap(subscriber.events) {
		monitor.publishLocked([]WorkspaceChange{{Kind: WorkspaceChangeRescan, Reason: "subscriber_replay_overflow"}})
		return &WorkspaceChangeSubscription{subscriber: subscriber}, nil
	}
	for _, event := range monitor.replay[start:] {
		subscriber.events <- cloneWorkspaceChangeEvent(event)
	}
	return &WorkspaceChangeSubscription{subscriber: subscriber}, nil
}

// Cursor returns the monitor's current replay position for health checks and
// deterministic handoff. It grants no filesystem access.
func (monitor *WorkspaceChangeMonitor) Cursor() WorkspaceChangeCursor {
	if monitor == nil {
		return WorkspaceChangeCursor{}
	}
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	return WorkspaceChangeCursor{SourceID: monitor.config.sourceID, Sequence: monitor.sequence}
}

func (subscription *WorkspaceChangeSubscription) Recv() (WorkspaceChangeEvent, error) {
	if subscription == nil || subscription.subscriber == nil {
		return WorkspaceChangeEvent{}, ErrWorkspaceChangeMonitorClosed
	}
	subscriber := subscription.subscriber
	select {
	case <-subscriber.ctx.Done():
		subscription.Close()
		return WorkspaceChangeEvent{}, subscriber.ctx.Err()
	case event, ok := <-subscriber.events:
		if ok {
			return event, nil
		}
		subscriber.mu.Lock()
		err := subscriber.err
		subscriber.mu.Unlock()
		if err == nil {
			err = ErrWorkspaceChangeMonitorClosed
		}
		return WorkspaceChangeEvent{}, err
	}
}

func (subscription *WorkspaceChangeSubscription) Close() {
	if subscription == nil || subscription.subscriber == nil {
		return
	}
	subscriber := subscription.subscriber
	monitor := subscriber.monitor
	if monitor == nil {
		return
	}
	monitor.mu.Lock()
	if current, exists := monitor.subscribers[subscriber.id]; exists && current == subscriber {
		delete(monitor.subscribers, subscriber.id)
		subscriber.failLocked(context.Canceled)
	}
	monitor.mu.Unlock()
}

func (subscriber *workspaceChangeSubscriber) failLocked(err error) {
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()
	if subscriber.closed {
		return
	}
	subscriber.closed = true
	subscriber.err = err
	close(subscriber.events)
}

func (monitor *WorkspaceChangeMonitor) Close() error {
	if monitor == nil {
		return nil
	}
	monitor.closeOnce.Do(func() {
		monitor.mu.Lock()
		monitor.closed = true
		for id, subscriber := range monitor.subscribers {
			subscriber.failLocked(ErrWorkspaceChangeMonitorClosed)
			delete(monitor.subscribers, id)
		}
		monitor.mu.Unlock()
		close(monitor.stop)
		monitor.closeErr = monitor.watcher.Close()
		monitor.wait.Wait()
	})
	return monitor.closeErr
}
