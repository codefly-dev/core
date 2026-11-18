package runstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// A persistent run's directory is never collected: a scope a developer can
// name again is a database somebody may come back to, and nothing here may
// decide otherwise. So every scope a developer ever ran and stopped leaves one
// for good, and only a person can say which are finished with. List is how
// they see them and Discard removes the one they name. Neither is called by a
// run, an agent or a sweep.

// State is one state directory a run is recorded on, as its records describe
// it now.
type State struct {
	Dir string
	Run Run
	// InUse is true while an agent still run by its launcher claims the
	// directory, a run that launched one of its agents still lives, a server
	// an agent started in it still runs, or a record cannot be read and so
	// cannot be ruled out.
	InUse bool
	// LastClaimed is when a run last claimed or released the directory: the
	// newest of its records. It says nothing about what the store wrote since.
	LastClaimed time.Time
}

// List reports every state directory under root that a run is recorded on and
// include accepts (nil accepts every run), persistent and disposable alike. It
// removes nothing and signals nothing. A directory no run is recorded on is
// not listed: nothing says whose it is. One whose records cannot be read is
// reported in the error and the listing goes on.
func List(root string, include func(Run) bool) ([]State, error) {
	recorded, failures := recordedDirs(root)
	var states []State
	for _, dir := range recorded {
		state, err := inspect(dir)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !state.claimed || (include != nil && !include(state.run)) {
			continue
		}
		claimed, err := lastClaimed(dir)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		states = append(states, State{Dir: dir, Run: state.run, InUse: state.inUse(), LastClaimed: claimed})
	}
	return states, errors.Join(failures...)
}

// inUse reports whether anything still uses the directory.
func (u usage) inUse() bool {
	return u.held || u.running || len(u.servers) > 0
}

func lastClaimed(dir string) (time.Time, error) {
	marker := filepath.Join(dir, markerDir)
	paths := []string{marker, filepath.Join(marker, runFile), filepath.Join(marker, holdersDir)}
	holders, err := os.ReadDir(filepath.Join(marker, holdersDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return time.Time{}, fmt.Errorf("read the records of %s: %w", dir, err)
	}
	for _, entry := range holders {
		paths = append(paths, filepath.Join(marker, holdersDir, entry.Name()))
	}
	var newest time.Time
	for _, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return time.Time{}, fmt.Errorf("read the records of %s: %w", dir, err)
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, nil
}

var (
	// ErrStateInUse refuses a directory something still uses.
	ErrStateInUse = errors.New("run state is in use")
	// ErrStateNotRecorded refuses a directory no run is recorded on.
	ErrStateNotRecorded = errors.New("no run is recorded on this directory")
)

// Discard removes the state directory of a run, persistent or not, because a
// person named it. It is the one removal of a persistent run's state, and the
// caller's statement is the whole of its authority: nothing may call it on a
// directory it chose by itself.
//
// A directory still in use (State.InUse) is refused with ErrStateInUse, and
// one no run is recorded on with ErrStateNotRecorded: it predates this
// package or belongs to something else, and naming it does not make it a
// run's. A directory already gone is not an error.
func Discard(dir string) error {
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	state, err := inspect(dir)
	if err != nil {
		return err
	}
	if !state.claimed {
		return fmt.Errorf("%w: %s", ErrStateNotRecorded, dir)
	}
	if state.inUse() {
		return fmt.Errorf("%w: %s", ErrStateInUse, dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove run state %s: %w", dir, err)
	}
	return nil
}
