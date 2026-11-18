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
// run that ever claimed it and none of those processes is left: no agent still
// run by its launcher, and no server an agent started there. The agent
// collects when its run destroys it, which is the ordinary end of a run; Sweep
// collects what a killed run could not, and is what `codefly clear` calls.
//
// A killed run also leaves its servers running, and they keep its directory in
// use for good. A caller that opts in (StopAbandoned, or
// SweepOptions.StopAbandoned) stops exactly those: the tracked processes of a
// disposable directory no live run claims any more.
//
// An agent that outlives its run notices (Launcher, Birth.Gone) and ends what
// that run left in the directories it claimed by the same rule
// (AbandonClaimed); see owner.go for who a run belongs to.
//
// A server several agents of a run share is found (Tracked) and stopped by the
// last agent to stop using it (Hold.StopTracked) through the same records.
//
// A directory nobody claimed is never touched: it predates this package or
// belongs to something else, and nothing here may infer the right to delete it
// (codefly-dev/core#424).
package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"time"

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

// mark is a process's birth as this package records it.
type mark = Birth

func markOf(pid int) (mark, error) { return BirthOf(pid) }

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

// holder is one agent using a state directory: the agent, the run that
// launched it, and the processes it left there that can outlive it.
type holder struct {
	Agent   mark   `json:"agent"`
	Run     *mark  `json:"run,omitempty"`
	Tracked []mark `json:"tracked,omitempty"`
}

// claims reports whether the holder's agent still uses the directory. An agent
// whose run is gone is orphaned and holds nothing by itself: its run was
// killed, and only what it started and still runs (Tracked) keeps the
// directory in use.
func (h holder) claims() bool {
	return h.Agent.alive() && (h.Run == nil || h.Run.alive())
}

// Hold is the calling agent's claim on a state directory.
type Hold struct {
	path   string
	holder holder
}

// Claim records the calling process, and the process that launched it, as a
// user of dir for run. The directory is the caller's: Claim creates only its
// own records inside it.
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
	// A file per claim, not per process: two users in one process release
	// independently. Until it is written it reads as a holder that cannot be
	// ruled out, which is the safe reading.
	file, err := os.CreateTemp(holders, strconv.Itoa(agent.PID)+"-*.json")
	if err != nil {
		return nil, fmt.Errorf("record the run using %s: %w", dir, err)
	}
	_ = file.Close()
	hold := &Hold{path: file.Name(), holder: holder{Agent: agent}}
	if parent := os.Getppid(); parent > 1 {
		if run, err := markOf(parent); err == nil {
			hold.holder.Run = &run
		}
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
	if !found || (recorded.Disposable && !run.Disposable) {
		if err := writeJSON(filepath.Join(dir, markerDir, runFile), run); err != nil {
			return nil, err
		}
	}
	remember(hold.path, dir)
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
	forget(h.path)
	h.path = ""
	return nil
}

func (h *Hold) write() error { return writeJSON(h.path, h.holder) }

// Collect removes dir when it is disposable and nothing uses it any more, and
// reports whether it did. Persistent and unclaimed directories are left alone.
func Collect(dir string) (bool, error) {
	state, err := inspect(dir)
	if err != nil || !state.collectable() {
		return false, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, fmt.Errorf("remove disposable run state %s: %w", dir, err)
	}
	return true, nil
}

// usage is what a state directory's records say about it now.
type usage struct {
	run Run
	// claimed is false for a directory no agent recorded a run on.
	claimed bool
	// held is true while an agent still run by its launcher claims the
	// directory, or a record cannot be read and so cannot be ruled out.
	held bool
	// running is true while a run that launched one of the directory's agents
	// still lives, even though that agent is gone: the run restarts its
	// agents, and what they left there is still its own.
	running bool
	// servers are the tracked processes still running.
	servers []mark
}

// collectable reports whether Collect may remove the directory.
func (u usage) collectable() bool {
	return u.claimed && u.run.Disposable && !u.held && len(u.servers) == 0
}

// abandoned reports whether the directory is a killed disposable run's whose
// servers were left running: its agents' runs are gone, nobody claims it, and
// only the servers keep it.
func (u usage) abandoned() bool {
	return u.claimed && u.run.Disposable && !u.held && !u.running && len(u.servers) > 0
}

// inspect reads a directory's run and who still uses it.
func inspect(dir string) (usage, error) {
	run, found, err := readRun(dir)
	state := usage{run: run, claimed: found}
	if err != nil || !found {
		return state, err
	}
	records, err := readHolders(dir, "")
	if err != nil {
		return state, err
	}
	for _, record := range records {
		h := record.holder
		// A record that cannot be read is a holder that cannot be ruled out.
		if !record.readable || h.claims() {
			state.held = true
			continue
		}
		if h.Run != nil && h.Run.alive() {
			state.running = true
		}
		for _, tracked := range h.Tracked {
			if tracked.alive() && !slices.Contains(state.servers, tracked) {
				state.servers = append(state.servers, tracked)
			}
		}
	}
	return state, nil
}

// StopOptions is how an abandoned run's servers are stopped.
type StopOptions struct {
	// Signal asks a server to stop. Zero means SIGTERM; a store whose orderly
	// shutdown is another signal names it.
	Signal syscall.Signal
	// Grace is how long the servers get to stop before they are killed. Zero
	// means ten seconds.
	Grace time.Duration
}

const (
	defaultStopGrace = 10 * time.Second
	// killWait is how long a killed process gets to disappear.
	killWait = 5 * time.Second
	stopPoll = 50 * time.Millisecond
)

// StopAbandoned stops the servers a killed disposable run left running in dir,
// and returns the PIDs it stopped. A killed run never destroys its agents, so
// nothing else stops what they started; until it is stopped the directory is
// in use and Collect keeps it. Call Collect afterwards.
//
// It acts only on a directory every claiming run declared disposable, whose
// agents' runs are all gone, and only on the processes recorded there with
// Hold.Track: each is asked to stop, given the grace period, then
// killed. A recorded PID that now names another process is that run's server
// gone, and is never signalled. Everything else is left exactly as it is: a
// persistent or unclaimed directory, a directory in use, the server of an
// agent that died inside a run that goes on, and any process not tracked
// there.
//
// A cancelled context ends the wait without killing anything.
func StopAbandoned(ctx context.Context, dir string, options StopOptions) ([]int, error) {
	state, err := inspect(dir)
	if err != nil || !state.abandoned() {
		return nil, err
	}
	return stop(ctx, dir, state.servers, options)
}

func stop(ctx context.Context, dir string, servers []mark, options StopOptions) ([]int, error) {
	signal, grace := options.Signal, options.Grace
	if signal == 0 {
		signal = syscall.SIGTERM
	}
	if grace <= 0 {
		grace = defaultStopGrace
	}
	var failures []error
	var signalled []mark
	send := func(server mark, signal syscall.Signal) {
		sent, err := server.signal(ctx, signal)
		if err != nil {
			failures = append(failures, fmt.Errorf("stop process %d left in %s: %w", server.PID, dir, err))
		} else if sent && !slices.Contains(signalled, server) {
			signalled = append(signalled, server)
		}
	}
	for _, server := range servers {
		send(server, signal)
	}
	left, err := waitGone(ctx, signalled, grace)
	if err != nil {
		return stopped(signalled, left), errors.Join(append(failures, err)...)
	}
	for _, server := range left {
		send(server, syscall.SIGKILL)
	}
	if left, err = waitGone(ctx, left, killWait); err != nil {
		failures = append(failures, err)
	}
	for _, server := range left {
		failures = append(failures, fmt.Errorf("process %d left in %s did not stop", server.PID, dir))
	}
	return stopped(signalled, left), errors.Join(failures...)
}

// signal delivers a signal to the marked birth and reports whether it was
// delivered; a process that is gone, or whose PID now names another process,
// receives nothing.
func (m mark) signal(ctx context.Context, signal syscall.Signal) (bool, error) {
	if m.PID == os.Getpid() || m.PID == os.Getppid() {
		return false, errors.New("it is this process or its launcher")
	}
	identity, err := runners.InspectProcess(m.PID)
	if errors.Is(err, runners.ErrProcessNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if identity.StartID != m.StartID || identity.BootID != m.BootID {
		return false, nil
	}
	// The kernel handle binds delivery to this birth even if the PID is
	// reused between the inspection and the signal.
	err = runners.SignalProcess(ctx, identity, signal)
	if errors.Is(err, runners.ErrProcessNotFound) {
		return false, nil
	}
	return err == nil, err
}

// waitGone waits up to budget for the marked processes to end and returns the
// ones still running.
func waitGone(ctx context.Context, servers []mark, budget time.Duration) ([]mark, error) {
	deadline := time.NewTimer(budget)
	defer deadline.Stop()
	poll := time.NewTicker(stopPoll)
	defer poll.Stop()
	for {
		servers = slices.DeleteFunc(slices.Clone(servers), func(server mark) bool { return !server.alive() })
		if len(servers) == 0 {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return servers, ctx.Err()
		case <-deadline.C:
			return servers, nil
		case <-poll.C:
		}
	}
}

// stopped is the PIDs of signalled that are not in left.
func stopped(signalled, left []mark) []int {
	var pids []int
	for _, server := range signalled {
		if !slices.Contains(left, server) {
			pids = append(pids, server.PID)
		}
	}
	return pids
}

// SweepOptions bounds a Sweep.
type SweepOptions struct {
	// Include keeps the sweep to the runs it accepts, which is how a caller
	// bounds it to one workspace. Nil means every run on the machine.
	Include func(Run) bool
	// DryRun reports what would be removed and removes nothing. With
	// StopAbandoned it stops nothing either, and reports what it would stop.
	DryRun bool
	// StopAbandoned makes the sweep also end what a killed run left running:
	// the servers of a disposable directory nobody claims any more are
	// stopped as StopAbandoned (the function) does, and the directory is then
	// collected. Nil, the default, never signals a process: a directory whose
	// server still runs is kept.
	StopAbandoned *StopOptions
}

// Swept is one state directory a Sweep removed, or would remove.
type Swept struct {
	Dir string
	Run Run
	// Stopped is the PIDs of the servers the sweep stopped in the directory,
	// or would stop on a dry run. Empty unless SweepOptions.StopAbandoned.
	Stopped []int
}

// Sweep collects every disposable state directory under root whose users are
// all gone: what runs that were killed rather than stopped left behind. A
// directory it cannot remove is reported in the error and the sweep goes on.
func Sweep(root string, options SweepOptions) ([]Swept, error) {
	var swept []Swept
	recorded, failures := recordedDirs(root)
	for _, child := range recorded {
		state, err := inspect(child)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if options.Include != nil && !options.Include(state.run) {
			continue
		}
		var stoppedPIDs []int
		if options.StopAbandoned != nil && state.abandoned() {
			if options.DryRun {
				for _, server := range state.servers {
					stoppedPIDs = append(stoppedPIDs, server.PID)
				}
			} else {
				stoppedPIDs, err = stop(context.Background(), child, state.servers, *options.StopAbandoned)
				if err != nil {
					failures = append(failures, err)
					continue
				}
				// Read again: the directory may have been claimed while
				// its servers stopped.
				if state, err = inspect(child); err != nil {
					failures = append(failures, err)
					continue
				}
				if !state.collectable() {
					continue
				}
			}
		} else if !state.collectable() {
			continue
		}
		if !options.DryRun {
			if err := os.RemoveAll(child); err != nil {
				failures = append(failures, fmt.Errorf("remove disposable run state %s: %w", child, err))
				continue
			}
		}
		swept = append(swept, Swept{Dir: child, Run: state.run, Stopped: stoppedPIDs})
	}
	return swept, errors.Join(failures...)
}

// recordedDirs is every directory under root that carries this package's
// records, down to sweepDepth, with what could not be read on the way. A
// directory without records is walked into, never returned.
func recordedDirs(root string) ([]string, []error) {
	var recorded []string
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
			recorded = append(recorded, child)
		}
	}
	walk(root, 1)
	return recorded, failures
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

// Tracked returns the processes recorded on dir with Hold.Track that still
// run, whichever agent recorded them and whether or not that agent is left.
// It is how the agents of a run that share one server find the server one of
// them started. A recorded PID that now names another process is not one.
func Tracked(dir string) ([]runners.ProcessIdentity, error) {
	records, err := readHolders(dir, "")
	if err != nil {
		return nil, err
	}
	var seen []mark
	var servers []runners.ProcessIdentity
	for _, record := range records {
		for _, tracked := range record.holder.Tracked {
			if slices.Contains(seen, tracked) {
				continue
			}
			seen = append(seen, tracked)
			identity, err := runners.InspectProcess(tracked.PID)
			if err == nil && identity.StartID == tracked.StartID && identity.BootID == tracked.BootID {
				servers = append(servers, identity)
			}
		}
	}
	return servers, nil
}

// StopTracked ends this claim's use of the processes it tracks, and returns
// the PIDs it stopped. It is the orderly stop of a server several agents of a
// run share: each agent calls it when its service stops, and the server stops
// with the last of them.
//
// A tracked process another live claim on the directory still tracks is left
// running: that agent serves from it. So is one a record that cannot be read
// may track. Every other is stopped as StopAbandoned stops a server: asked,
// given the grace period, then killed, and never signalled unless it is still
// the birth that was tracked. This is the agent stopping what its own run
// started, so it applies to a developer's directory as to a disposable one.
//
// The claim itself stays: the agent still uses the directory, and tracks the
// server again if it starts again. After an error it still tracks what it
// tracked.
func (h *Hold) StopTracked(ctx context.Context, options StopOptions) ([]int, error) {
	if h == nil || h.path == "" || len(h.holder.Tracked) == 0 {
		return nil, nil
	}
	dir := filepath.Dir(filepath.Dir(filepath.Dir(h.path)))
	others, err := readHolders(dir, h.path)
	if err != nil {
		return nil, err
	}
	var unused []mark
	for _, tracked := range h.holder.Tracked {
		used := slices.ContainsFunc(others, func(other holderRecord) bool {
			return !other.readable || (other.holder.claims() && slices.Contains(other.holder.Tracked, tracked))
		})
		if !used && tracked.alive() {
			unused = append(unused, tracked)
		}
	}
	pids, err := stop(ctx, dir, unused, options)
	if err != nil {
		return pids, err
	}
	h.holder.Tracked = nil
	return pids, h.write()
}

// holderRecord is one holder file of a state directory.
type holderRecord struct {
	holder holder
	// readable is false for a record that could not be read: a claim being
	// written, or a holder that cannot be ruled out.
	readable bool
}

// readHolders reads the holder records of dir, except the one at skip.
func readHolders(dir, skip string) ([]holderRecord, error) {
	holders := filepath.Join(dir, markerDir, holdersDir)
	entries, err := os.ReadDir(holders)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read the holders of %s: %w", dir, err)
	}
	var records []holderRecord
	for _, entry := range entries {
		path := filepath.Join(holders, entry.Name())
		// An interrupted write leaves its temporary file, which is no holder.
		if filepath.Ext(entry.Name()) != ".json" || path == skip {
			continue
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		var record holderRecord
		record.readable = err == nil && json.Unmarshal(data, &record.holder) == nil
		records = append(records, record)
	}
	return records, nil
}
