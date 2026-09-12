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

// shouldReapContainer decides whether a codefly-owned container is garbage to
// remove. The rules, in order:
//
//   - ledgered → keep, unconditionally. A container carrying an invocation id
//     belongs to a session ledger that records whether its data must survive a
//     stop. This sweep cannot see that record, and its "owner dead + stopped →
//     reap" rule is exactly wrong for one: stopping a data container is how the
//     ledger RETAINS it, so reaping it here deletes the database the ledger
//     just promised to keep. Ledgered containers are disposed of by
//     sessionledger recovery, which reads the ownership record.
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
func shouldReapContainer(state string, ownerAlive, ephemeral, ledgered bool) bool {
	if ledgered {
		return false
	}
	if ownerAlive {
		return false
	}
	if state == "running" && !ephemeral {
		return false
	}
	return true
}

// ReapStaleContainers recovers containers labeled for this exact scope. Recovery
// across scopes belongs to ReapDisposableContainers, which is authorized by the
// durable namespace label rather than by a naming-scope prefix.
// Legacy containers without scope labels require explicit owner recovery.
func ReapStaleContainers(ctx context.Context, scope ContainerRecoveryScope) error {
	if scope.id == "" {
		return fmt.Errorf("container recovery scope is unresolved")
	}
	w := wool.Get(ctx).In("base.ReapStaleContainers")

	cli, _, err := newDockerClient()
	if err != nil {
		// Docker not reachable — nothing to reap, not an error.
		w.Trace("docker client unavailable — skipping container sweep", wool.ErrField(err))
		return nil
	}
	defer cli.Close()
	// Constructing a Docker client does not establish a connection. Probe the
	// resolved daemon before listing so an installed but stopped/unresponsive
	// engine remains the documented no-op instead of emitting a misleading
	// sweep warning on runs that resolve entirely to Local or Nix backends.
	if err := pingDockerClient(ctx, cli); err != nil {
		w.Trace("docker engine unavailable — skipping container sweep", wool.ErrField(err))
		return nil
	}

	// Filter containers by the codefly owner label so we only touch ones
	// we created. Using labels (not name prefix) is authoritative: a user
	// could rename a container, but the label sticks.
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	containers, err := cli.ContainerList(listCtx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", LabelCodeflyOwner+"="+labelTrue),
			filters.Arg("label", LabelCodeflyRecoveryScope+"="+scope.id),
		),
	})
	if err != nil {
		return fmt.Errorf("cannot list codefly containers: %w", err)
	}
	reaped := 0
	for _, c := range containers {
		if !staleContainerInScope(c, scope) {
			continue
		}

		w.Warn("reaping orphaned container in recovery scope",
			wool.Field("container", c.ID[:12]),
			wool.Field("name", c.Labels[LabelCodeflyName]),
			wool.Field("state", c.State),
			wool.Field("session_pid", c.Labels[LabelCodeflySession]))

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

func staleContainerInScope(c container.Summary, scope ContainerRecoveryScope) bool {
	if scope.id == "" || c.Labels[LabelCodeflyOwner] != labelTrue || c.Labels[LabelCodeflyRecoveryScope] != scope.id {
		return false
	}
	// A namespace label naming another host's home/workspace is never ours, and
	// neither is one we cannot verify because this host has no durable identity.
	// A container created before the label existed carries none at all: Docker
	// cannot add a label to a container that already exists, so refusing those
	// would strand every pre-upgrade container forever. For them the exact scope
	// hash — home, workspace and naming scope — remains the authority it was.
	if namespace := c.Labels[LabelCodeflyRecoveryNamespace]; namespace != scope.namespace && namespace != "" {
		return false
	}
	pid, err := strconv.Atoi(c.Labels[LabelCodeflySession])
	// IsProcessAlive intentionally rejects system PIDs. That is not evidence
	// that PID 1 is dead: an agent/library caller may be its namespace's init.
	if err != nil || pid <= 1 {
		return false
	}
	return shouldReapContainer(c.State, base.IsProcessAlive(pid), c.Labels[LabelCodeflyEphemeral] == labelTrue, c.Labels[LabelCodeflyInvocation] != "")
}
