// Package runstate is the one lifecycle for the native state a run leaves on
// the machine: the directory a store's agent keeps a cluster, a data file or a
// configuration in when it runs the store as a host process instead of a
// container.
//
// Three parties are involved and each knows one thing. The agent owns the
// directory: it chooses where it is and what is in it. The run's caller knows
// whether the state is throwaway: an invocation that generated its own naming
// scope (temporary ports, a test-owned dependency stack) can never be run
// again, while a scope a developer named, or no scope at all, is that
// developer's database. And only the machine knows whether the processes that
// used a directory are still there.
//
// So the caller states disposability once, in the environment it hands every
// agent (base.v0.Environment.disposable); the agent records it on the
// directory together with the processes using it (Claim, Hold.Track); and a
// directory is removed (Collect) only when it was declared disposable by every
// run that ever claimed it and none of those processes is left. The agent
// collects when its run destroys it, which is the ordinary end of a run; Sweep
// collects what a killed run could not, and is what `codefly clear` calls.
//
// A directory nobody claimed is never touched: it predates this package or
// belongs to something else, and nothing here may infer the right to delete it
// (codefly-dev/core#424).
package runstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runners "github.com/codefly-dev/core/runners/base"
)

const (
	// markerDir holds this package's records inside a state directory.
	markerDir  = ".codefly-run"
	runFile    = "run.json"
	holdersDir = "holders"

	// sweepDepth is how far below the root a state directory may sit:
	// <root>/<store>/<run> or <root>/<store>/<group>/<run>.
	sweepDepth = 3
)

// Root is the machine directory native state lives under:
// <user cache dir>/codefly. Agents place their directories below it, one
// directory per store kind, so that Sweep finds them.
func Root() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate the user cache directory: %w", err)
	}
	return filepath.Join(cache, "codefly"), nil
}

// Run is the run a state directory belongs to, as the agent was told it.
type Run struct {
	// Workspace is the workspace's path.
	Workspace string `json:"workspace"`
	// NamingScope is the run's naming scope, empty for a plain run.
	NamingScope string `json:"naming_scope,omitempty"`
	// Disposable is the caller's statement that the state is throwaway.
	Disposable bool `json:"disposable"`
}

// For is the run an agent was loaded for: its workspace's path and the
// environment the caller handed it. Disposability needs a naming scope: the
// unscoped run of a workspace is the developer's own, whatever else is said.
func For(workspacePath string, environment *basev0.Environment) Run {
	scope := environment.GetNamingScope()
	return Run{
		Workspace:   workspacePath,
		NamingScope: scope,
		Disposable:  environment.GetDisposable() && scope != "",
	}
}

// mark is a process's birth: the PID plus the facts that tell it from a later
// process reusing the number.
type mark struct {
	PID     int    `json:"pid"`
	StartID uint64 `json:"start"`
	BootID  string `json:"boot"`
}

func markOf(pid int) (mark, error) {
	identity, err := runners.InspectProcess(pid)
	if err != nil {
		return mark{}, err
	}
	return mark{PID: pid, StartID: identity.StartID, BootID: identity.BootID}, nil
}

// alive reports whether the marked process still runs. An inspection that
// fails for any reason other than the process being gone counts as alive: a
// wrong "dead" deletes the data of a live run.
func (m mark) alive() bool {
	identity, err := runners.InspectProcess(m.PID)
	if err != nil {
		return !errors.Is(err, runners.ErrProcessNotFound)
	}
	return identity.StartID == m.StartID && identity.BootID == m.BootID
}

// holder is one agent using a state directory, and the processes it left
// there that can outlive it.
type holder struct {
	Agent   mark   `json:"agent"`
	Tracked []mark `json:"tracked,omitempty"`
}

func (h holder) alive() bool {
	if h.Agent.alive() {
		return true
	}
	for _, tracked := range h.Tracked {
		if tracked.alive() {
			return true
		}
	}
	return false
}

// Hold is the calling agent's claim on a state directory.
type Hold struct {
	path   string
	holder holder
}

// Claim records the calling process as a user of dir for run. The directory is
// the caller's: Claim creates only its own records inside it.
//
// A directory stays disposable only while every run that claims it says so. A
// persistent claim is final, because a scope a developer can name again is a
// database somebody may come back to.
func Claim(dir string, run Run) (*Hold, error) {
	agent, err := markOf(os.Getpid())
	if err != nil {
		return nil, fmt.Errorf("inspect this process: %w", err)
	}
	holders := filepath.Join(dir, markerDir, holdersDir)
	if err := os.MkdirAll(holders, 0o700); err != nil {
		return nil, fmt.Errorf("record the run using %s: %w", dir, err)
	}
	hold := &Hold{
		path:   filepath.Join(holders, strconv.Itoa(agent.PID)+"-"+strconv.FormatUint(agent.StartID, 10)+".json"),
		holder: holder{Agent: agent},
	}
	// The holder first: once the directory can read as disposable it already
	// has a live user.
	if err := hold.write(); err != nil {
		return nil, err
	}
	recorded, found, err := readRun(dir)
	if err != nil {
		return nil, err
	}
	if found && (!recorded.Disposable || run.Disposable) {
		return hold, nil
	}
	if err := writeJSON(filepath.Join(dir, markerDir, runFile), run); err != nil {
		return nil, err
	}
	return hold, nil
}

// Track adds a process this agent started in the directory and that does not
// die with it, such as a detached database server. The directory is in use
// for as long as that process runs.
func (h *Hold) Track(pid int) error {
	tracked, err := markOf(pid)
	if err != nil {
		return fmt.Errorf("inspect process %d: %w", pid, err)
	}
	for _, known := range h.holder.Tracked {
		if known == tracked {
			return nil
		}
	}
	h.holder.Tracked = append(h.holder.Tracked, tracked)
	return h.write()
}

// Release withdraws the claim. The caller has stopped what it tracked, or
// another holder still tracks it.
func (h *Hold) Release() error {
	if h == nil || h.path == "" {
		return nil
	}
	if err := os.Remove(h.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	h.path = ""
	return nil
}

func (h *Hold) write() error { return writeJSON(h.path, h.holder) }

// Collect removes dir when it is disposable and nothing uses it any more, and
// reports whether it did. Persistent and unclaimed directories are left alone.
func Collect(dir string) (bool, error) {
	_, collectable, err := inspect(dir)
	if err != nil || !collectable {
		return false, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, fmt.Errorf("remove disposable run state %s: %w", dir, err)
	}
	return true, nil
}

// inspect returns a claimed directory's run and whether Collect may remove it.
func inspect(dir string) (Run, bool, error) {
	run, found, err := readRun(dir)
	if err != nil || !found || !run.Disposable {
		return run, false, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, markerDir, holdersDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return run, false, fmt.Errorf("read the holders of %s: %w", dir, err)
	}
	for _, entry := range entries {
		// An interrupted write leaves its temporary file, which is no holder.
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, markerDir, holdersDir, entry.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		var h holder
		// A record that cannot be read is a holder that cannot be ruled out.
		if err != nil || json.Unmarshal(data, &h) != nil || h.alive() {
			return run, false, nil
		}
	}
	return run, true, nil
}

// SweepOptions bounds a Sweep.
type SweepOptions struct {
	// Workspace keeps the sweep to the runs of one workspace path. Empty means
	// every workspace on the machine.
	Workspace string
	// DryRun reports what would be removed and removes nothing.
	DryRun bool
}

// Swept is one state directory a Sweep removed, or would remove.
type Swept struct {
	Dir string
	Run Run
}

// Sweep collects every disposable state directory under root whose users are
// all gone: what runs that were killed rather than stopped left behind. A
// directory it cannot remove is reported in the error and the sweep goes on.
func Sweep(root string, options SweepOptions) ([]Swept, error) {
	var swept []Swept
	var failures []error
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, err)
			}
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			child := filepath.Join(dir, entry.Name())
			if _, err := os.Lstat(filepath.Join(child, markerDir)); err != nil {
				if depth < sweepDepth {
					walk(child, depth+1)
				}
				continue
			}
			run, collectable, err := inspect(child)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if !collectable || (options.Workspace != "" && run.Workspace != options.Workspace) {
				continue
			}
			if !options.DryRun {
				if err := os.RemoveAll(child); err != nil {
					failures = append(failures, fmt.Errorf("remove disposable run state %s: %w", child, err))
					continue
				}
			}
			swept = append(swept, Swept{Dir: child, Run: run})
		}
	}
	walk(root, 1)
	return swept, errors.Join(failures...)
}

func readRun(dir string) (Run, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, markerDir, runFile))
	if errors.Is(err, os.ErrNotExist) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, fmt.Errorf("read the run recorded on %s: %w", dir, err)
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return Run{}, false, fmt.Errorf("read the run recorded on %s: %w", dir, err)
	}
	return run, true, nil
}

// writeJSON replaces path atomically with an owner-only file.
func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err = temporary.Write(data); err == nil {
		err = temporary.Chmod(0o600)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
