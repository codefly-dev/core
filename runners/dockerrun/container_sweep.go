package dockerrun

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"

	"github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/wool"
)

// Container orphan sweep — the Docker-mode analog of ReapStaleProcessGroups.
//
// Native/Nix modes track orphans via pgid files under ~/.codefly/runs/.
// Docker containers can't participate in pgid tracking — process groups
// are namespaced inside the container, so from the host they look like
// a single daemon-managed resource. Instead we label every codefly-owned
// container with the spawning CLI's PID (LabelCodeflySession) at create
// time; on the next `codefly run` startup, this sweep lists all such
// containers and removes the ones whose owning CLI is dead.
//
// This is the adapted Ryuk pattern: same labeled-cleanup idea, but no
// sidecar process. Ryuk earns its keep in parallel test runs (it also
// handles the "many CLIs" case via a socket heartbeat); for codefly's
// single-machine CLI, startup sweep is sufficient and matches the
// existing orphan-reap posture.

// ReapStaleContainers lists all containers carrying LabelCodeflyOwner
// and removes the ones whose LabelCodeflySession PID is no longer alive.
// Best-effort: a single failed remove is logged and the sweep continues.
// Safe to call when Docker isn't running — returns nil (just no sweep).
// shouldReapContainer decides whether a codefly-owned container is garbage to
// remove. The rules, in order:
//
//   - owner still alive   → keep (actively managed by a live CLI).
//   - owner dead, stopped → reap (orphaned, useless to anyone).
//   - owner dead, running, NOT ephemeral → keep. This is the "reuse stateful
//     services (postgres/redis) by name across CLI restarts" pattern;
//     killing them would nuke dev data the next `codefly run` reuses.
//     (Stateless infra like vault dev-mode opts out via WithEphemeral — it has
//     no data to preserve, and a lingering vault holds its port / bleeds stale
//     state into the next run.)
//   - owner dead, running, ephemeral → reap. SDK/`--cli-server` (test)
//     dependencies use a unique per-run naming scope and are NEVER reused, so
//     a running one with a dead owner is pure garbage. Not reaping these is
//     what leaked 28 Neo4j/Postgres containers across a day of killed test
//     runs and blew up OrbStack's memory.
func shouldReapContainer(state string, ownerAlive, ephemeral bool) bool {
	if ownerAlive {
		return false
	}
	if state == "running" && !ephemeral {
		return false
	}
	return true
}

func ReapStaleContainers(ctx context.Context) error {
	w := wool.Get(ctx).In("base.ReapStaleContainers")

	cli, _, err := newDockerClient()
	if err != nil {
		// Docker not reachable — nothing to reap, not an error.
		w.Trace("docker client unavailable — skipping container sweep", wool.ErrField(err))
		return nil
	}
	defer cli.Close()

	// Filter containers by the codefly owner label so we only touch ones
	// we created. Using labels (not name prefix) is authoritative: a user
	// could rename a container, but the label sticks.
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	containers, err := cli.ContainerList(listCtx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", LabelCodeflyOwner+"=true"),
		),
	})
	if err != nil {
		return fmt.Errorf("cannot list codefly containers: %w", err)
	}

	reaped := 0
	for _, c := range containers {
		sessionStr := c.Labels[LabelCodeflySession]
		if sessionStr == "" {
			continue // older unlabeled container — don't touch
		}
		pid, err := strconv.Atoi(sessionStr)
		if err != nil || pid <= 0 {
			continue // malformed label — conservative: leave it
		}
		ephemeral := c.Labels[LabelCodeflyEphemeral] == "true"
		if !shouldReapContainer(c.State, base.IsProcessAlive(pid), ephemeral) {
			continue
		}

		w.Warn("reaping orphaned stopped container",
			wool.Field("container", c.ID[:12]),
			wool.Field("name", c.Labels[LabelCodeflyName]),
			wool.Field("state", c.State),
			wool.Field("session_pid", pid))

		// Short bounded context per remove — one unresponsive container
		// shouldn't stall the whole sweep.
		rmCtx, rmCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if rmErr := cli.ContainerRemove(rmCtx, c.ID, container.RemoveOptions{Force: true}); rmErr != nil {
			w.Warn("cannot remove stale container",
				wool.Field("container", c.ID[:12]), wool.ErrField(rmErr))
		} else {
			reaped++
		}
		rmCancel()
	}
	if reaped > 0 {
		w.Info("reaped stale containers", wool.Field("count", reaped))
	}
	return nil
}
