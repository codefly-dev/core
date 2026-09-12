package dockerrun

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
)

// ReapDisposableContainers recovers ephemeral containers across invocation
// scopes, using their durable canonical home/workspace namespace label. It
// never broadens recovery of stateful, legacy or session-ledger containers.
func ReapDisposableContainers(ctx context.Context, scope ContainerRecoveryScope) error {
	if scope.id == "" || scope.namespace == "" {
		return errors.New("container recovery scope is unresolved")
	}
	cli, _, err := newDockerClient()
	if err != nil {
		return ctx.Err()
	}
	defer func() { _ = cli.Close() }()
	if err = pingDockerClient(ctx, cli); err != nil {
		return ctx.Err() // Docker retains the ownership labels for the next run.
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

func disposableContainerInNamespace(c container.Summary, scope ContainerRecoveryScope) bool {
	return scope.namespace != "" && c.Labels[LabelCodeflyRecoveryNamespace] == scope.namespace &&
		c.Labels[LabelCodeflyEphemeral] == labelTrue &&
		staleContainerInScope(c, ContainerRecoveryScope{id: c.Labels[LabelCodeflyRecoveryScope]})
}
