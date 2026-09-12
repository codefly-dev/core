package dockerrun

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"

	"github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/wool"
)

// ReapDisposableContainers recovers ephemeral containers across invocation
// scopes, using their durable canonical home/workspace namespace label. It
// never broadens recovery of stateful, legacy or session-ledger containers.
func ReapDisposableContainers(ctx context.Context, scope ContainerRecoveryScope) error {
	if scope.id == "" {
		return errors.New("container recovery scope is unresolved")
	}
	w := wool.Get(ctx).In("base.ReapDisposableContainers")
	if scope.namespace == "" {
		// The host could not prove a durable identity, so nothing here can be
		// attributed across scopes. Say so: the leak this sweep exists to collect
		// silently resumes otherwise.
		w.Warn("cross-scope disposable recovery unavailable without a durable host identity; only exact-scope recovery will run")
		return nil
	}
	cli, _, err := newDockerClient()
	if err != nil {
		// Docker not reachable — nothing to reap, not an error. Trace it anyway:
		// a misconfigured daemon otherwise reads as "swept, nothing found".
		w.Trace("docker client unavailable — skipping disposable recovery", wool.ErrField(err))
		return ctx.Err()
	}
	defer func() { _ = cli.Close() }()
	if err = pingDockerClient(ctx, cli); err != nil {
		// Docker retains the ownership labels for the next run.
		w.Trace("docker engine unavailable — skipping disposable recovery", wool.ErrField(err))
		return ctx.Err()
	}
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	containers, err := cli.ContainerList(listCtx, container.ListOptions{All: true, Filters: filters.NewArgs(
		filters.Arg("label", LabelCodeflyOwner+"="+labelTrue),
		filters.Arg("label", LabelCodeflyRecoveryNamespace+"="+scope.namespace),
		filters.Arg("label", LabelCodeflyEphemeral+"="+labelTrue),
	)})
	cancel()
	if err != nil {
		return fmt.Errorf("list disposable containers: %w", err)
	}
	var failures []error
	for _, c := range containers {
		if !disposableContainerInNamespace(c, scope) {
			continue
		}
		removeCtx, removeCancel := context.WithTimeout(ctx, 10*time.Second)
		err := cli.ContainerRemove(removeCtx, c.ID, container.RemoveOptions{Force: true})
		removeCancel()
		if err != nil && !errdefs.IsNotFound(err) {
			failures = append(failures, fmt.Errorf("recover disposable container %s: %w", c.ID, err))
		}
	}
	return errors.Join(append(failures, ctx.Err())...)
}

// disposableContainerInNamespace states the cross-scope rules directly rather
// than borrowing staleContainerInScope with a scope built from the container's
// own labels, which made that predicate's exact-scope comparison tautological
// and hid which checks were really gating removal.
func disposableContainerInNamespace(c container.Summary, scope ContainerRecoveryScope) bool {
	if scope.namespace == "" ||
		c.Labels[LabelCodeflyOwner] != labelTrue ||
		c.Labels[LabelCodeflyRecoveryScope] == "" ||
		c.Labels[LabelCodeflyRecoveryNamespace] != scope.namespace ||
		c.Labels[LabelCodeflyEphemeral] != labelTrue {
		return false
	}
	pid, err := strconv.Atoi(c.Labels[LabelCodeflySession])
	// IsProcessAlive intentionally rejects system PIDs. That is not evidence
	// that PID 1 is dead: an agent/library caller may be its namespace's init.
	if err != nil || pid <= 1 {
		return false
	}
	// Ephemeral is established above; a ledgered container stays with
	// sessionledger recovery, which can read its retention record.
	return shouldReapContainer(c.State, base.IsProcessAlive(pid), true, c.Labels[LabelCodeflyInvocation] != "")
}
