package golang

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/codefly-dev/core/resources"
	runners "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/runners/companion"
)

// gitMaintenanceConfig is what the runner imposes on every git the go tool
// runs in the shared module cache: no automatic repository maintenance.
//
// A `git fetch` ends by launching `git maintenance run --auto --detach`, which
// repacks the repository it just fetched into in a process that outlives the
// fetch. When the go tool resolves a pseudo-version it reaches the commit with
// a shallow fetch and then, to validate the base tag the pseudo-version names,
// deepens the clone: `git fetch` of every ref, then `git fetch --unshallow`.
// The maintenance the first fetch detached rewrites .git/shallow while the
// unshallow is reading it, and git refuses the fetch:
//
//	fatal: shallow file has changed since we read it
//
// The go tool's lock on the clone does not cover the detached child. Both
// settings are named because the task is reached two ways: git >= 2.30 runs it
// as the `maintenance` command, older gits as `gc --auto`. This is the same
// configuration codefly-dev/cli#890 gives its own module prefetch.
var gitMaintenanceConfig = [][2]string{
	{"maintenance.auto", "false"},
	{"gc.auto", "0"},
}

// gitMaintenanceOffEnv is the GIT_CONFIG_* entries that append
// gitMaintenanceConfig after the git configuration base already passes in the
// environment: a GIT_CONFIG_KEY_<n> and GIT_CONFIG_VALUE_<n> for each setting,
// numbered from the host's GIT_CONFIG_COUNT, then the new GIT_CONFIG_COUNT.
//
// They are layered over base, where the last value of a key wins, so the
// host's entries below its count are kept and read first, and a stale entry at
// an index this appends is overwritten. A host count git itself would reject
// counts as none.
func gitMaintenanceOffEnv(base []string) []string {
	configured := hostGitConfigCount(base)
	env := make([]string, 0, 2*len(gitMaintenanceConfig)+1)
	for _, setting := range gitMaintenanceConfig {
		env = append(env,
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", configured, setting[0]),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", configured, setting[1]),
		)
		configured++
	}
	return append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(configured))
}

// hostGitConfigCount is the GIT_CONFIG_COUNT base passes, and 0 when it passes
// none or a value git would reject. The last entry wins, as it does for exec.
func hostGitConfigCount(base []string) int {
	count := 0
	for _, entry := range base {
		value, ok := strings.CutPrefix(entry, "GIT_CONFIG_COUNT=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 {
			n = 0
		}
		count = n
	}
	return count
}

// newGoProcess is a `go` process in the runner's environment with automatic
// git maintenance turned off, for every go command that may fetch into the
// shared module cache: download, build, test and vet all resolve modules.
func (r *GoRunnerEnvironment) newGoProcess(ctx context.Context, args ...string) (runners.Proc, error) {
	proc, err := r.Env().NewProcess("go", args...)
	if err != nil {
		return nil, err
	}
	var base []string
	if r.inheritsHostEnvironment() {
		base = os.Environ()
	}
	for _, entry := range gitMaintenanceOffEnv(base) {
		key, value, _ := strings.Cut(entry, "=")
		proc.WithEnvironmentVariables(ctx, resources.Env(key, value))
	}
	return proc, nil
}

// inheritsHostEnvironment reports whether a process this runner starts sees
// the host's environment, and so the host's GIT_CONFIG_* entries. A native
// process starts from os.Environ; a Docker container and a Nix devShell start
// from their own, so the runner's entries are numbered from zero there — a
// count naming host keys the process cannot see would make git refuse to run.
func (r *GoRunnerEnvironment) inheritsHostEnvironment() bool {
	if r.companion != nil {
		return r.companion.Backend() == companion.BackendLocal
	}
	return r.local != nil
}
