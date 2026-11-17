package runstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	runners "github.com/codefly-dev/core/runners/base"
)

// A run belongs to the process that started it, and ends with it. Two hops
// carry that: the caller that starts a run for itself (the SDK starting a
// dependency stack for a test) names itself in the run's environment
// (OwnedBy, Owner), and an agent's owner is the run that launched it
// (Launcher). Each side watches the other's birth (Birth.Gone) and, once it is
// gone, stops in order as if it had been asked to: nothing else ever would,
// because a killed process stops nothing. What an agent leaves then is decided
// by the same records as everything else here (AbandonClaimed).

// OwnerEnvironment carries the birth of the process a run was started for, in
// the environment of that run and of nothing below it. A run that reads it
// stops when that process is gone. A run nobody owns (a developer's, or a
// stack kept running for the next caller) has none.
const OwnerEnvironment = "CODEFLY_RUN_OWNER"

// ownerPoll is how often a watched birth is looked at.
const ownerPoll = 250 * time.Millisecond

// Birth names a process by its birth: the PID plus the facts that tell it from
// a later process reusing the number.
type Birth struct {
	PID     int    `json:"pid"`
	StartID uint64 `json:"start"`
	BootID  string `json:"boot"`
}

// BirthOf is the birth of the process running as pid now.
func BirthOf(pid int) (Birth, error) {
	identity, err := runners.InspectProcess(pid)
	if err != nil {
		return Birth{}, err
	}
	return Birth{PID: pid, StartID: identity.StartID, BootID: identity.BootID}, nil
}

// Launcher is the birth of the process that started this one, and whether
// there is one: a process already reparented to the machine's init has none.
func Launcher() (Birth, bool) {
	parent := os.Getppid()
	if parent <= 1 {
		return Birth{}, false
	}
	launcher, err := BirthOf(parent)
	// Read again: a launcher that ended while it was inspected left this
	// process to another parent, and that one's birth is not the launcher's.
	if err != nil || os.Getppid() != parent {
		return Birth{}, false
	}
	return launcher, true
}

// Alive reports whether the process born as b still runs. An inspection that
// fails for any reason other than the process being gone counts as alive.
func (b Birth) Alive() bool { return b.alive() }

// Gone returns a channel that is closed once the process born as b has ended,
// however it ended. It stays open for good when ctx ends first. Nothing is
// ever signalled: a later process under the same PID is not b, and b is then
// gone.
func (b Birth) Gone(ctx context.Context) <-chan struct{} {
	gone := make(chan struct{})
	// The kernel answers for a parent without a race: this process has
	// another parent the moment its own ends, even while the dead one still
	// waits to be reaped and can be inspected.
	parent := os.Getppid() == b.PID
	go func() {
		poll := time.NewTicker(ownerPoll)
		defer poll.Stop()
		for ctx.Err() == nil {
			if (parent && os.Getppid() != b.PID) || !b.alive() {
				close(gone)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-poll.C:
			}
		}
	}()
	return gone
}

// String is the form a birth travels in: "<pid>:<start>:<boot>".
func (b Birth) String() string {
	return strconv.Itoa(b.PID) + ":" + strconv.FormatUint(b.StartID, 10) + ":" + b.BootID
}

func parseBirth(value string) (Birth, error) {
	parts := strings.SplitN(value, ":", 3)
	if len(parts) != 3 {
		return Birth{}, fmt.Errorf("a process birth is <pid>:<start>:<boot>, not %q", value)
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil || pid <= 1 {
		return Birth{}, fmt.Errorf("process birth %q names no process", value)
	}
	start, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || start == 0 || parts[2] == "" {
		return Birth{}, fmt.Errorf("process birth %q does not say when the process was born", value)
	}
	return Birth{PID: pid, StartID: start, BootID: parts[2]}, nil
}

// Unowned returns environment without an owner: what a run nobody owns is
// started with, and what a run hands to the processes it starts in turn, which
// are its own and not its owner's.
func Unowned(environment []string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, OwnerEnvironment+"=") {
			result = append(result, entry)
		}
	}
	return result
}

// OwnedBy returns environment with owner declared as the process the run
// started with it belongs to, replacing any inherited declaration.
func OwnedBy(environment []string, owner Birth) []string {
	return append(Unowned(environment), OwnerEnvironment+"="+owner.String())
}

// Owner reads the process this run was started for from its environment, and
// whether one was declared. A declaration that cannot be read is an error, not
// an unowned run: such a run would outlive the process that said it owns it.
func Owner(environment []string) (Birth, bool, error) {
	declared, found := "", false
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, OwnerEnvironment+"="); ok {
			declared, found = value, true
		}
	}
	if !found {
		return Birth{}, false, nil
	}
	owner, err := parseBirth(declared)
	if err != nil {
		return Birth{}, false, fmt.Errorf("read the owner of this run from %s: %w", OwnerEnvironment, err)
	}
	return owner, true, nil
}

// claimed are the directories this process holds a claim on, by claim.
var claimed struct {
	sync.Mutex
	dirs map[string]string
}

func remember(claim, dir string) {
	claimed.Lock()
	defer claimed.Unlock()
	if claimed.dirs == nil {
		claimed.dirs = make(map[string]string)
	}
	claimed.dirs[claim] = dir
}

func forget(claim string) {
	claimed.Lock()
	defer claimed.Unlock()
	delete(claimed.dirs, claim)
}

// AbandonClaimed is what an agent does with the directories it claimed once
// the run that launched it is gone, and it returns the directories it removed.
// The agent has stopped what it runs, as it does when it is asked to; this
// ends what is left of a killed disposable run in those directories exactly as
// StopAbandoned and Collect decide it, and nothing else: a persistent
// directory, one another run still uses, and one whose run in fact still
// lives are left as they are. The agent is expected to exit afterwards.
func AbandonClaimed(ctx context.Context, options StopOptions) ([]string, error) {
	claimed.Lock()
	var dirs []string
	for _, dir := range claimed.dirs {
		if !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	claimed.Unlock()
	var removed []string
	var failures []error
	for _, dir := range dirs {
		if _, err := StopAbandoned(ctx, dir, options); err != nil {
			failures = append(failures, err)
			continue
		}
		collected, err := Collect(dir)
		if err != nil {
			failures = append(failures, err)
		}
		if collected {
			removed = append(removed, dir)
		}
	}
	return removed, errors.Join(failures...)
}
