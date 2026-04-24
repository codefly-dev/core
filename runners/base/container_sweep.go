package base

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"

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
func ReapStaleContainers(ctx context.Context) error {
	w := wool.Get(ctx).In("base.ReapStaleContainers")

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
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
		if isProcessAlive(pid) {
			continue // owning session still running; not an orphan
		}

		// Only reap STOPPED/EXITED containers. Running containers with a
		// dead owner are the codefly "reuse-by-name across CLI restarts"
		// pattern — stateful services like postgres/redis/vault survive
		// CLI death intentionally (docker daemon manages them) and the
		// next agent start will reuse them by name to preserve state.
		// Killing them here would mean every `codefly run` nukes postgres
		// data; we only want to clean up *dead* containers that aren't
		// useful to anyone.
		if c.State == "running" {
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
