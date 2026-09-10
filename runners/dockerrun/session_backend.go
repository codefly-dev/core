package dockerrun

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"

	"github.com/codefly-dev/core/sessionledger"
)

// Session-ledger resource kinds and backend name for Docker.
const (
	// BackendDocker is the adapter name recorded in the ledger.
	BackendDocker = "docker"
	// KindContainer is a container: execution that may also hold data in its
	// writable layer.
	KindContainer = "docker.container"
	// KindVolume is a named volume: data with no execution of its own.
	KindVolume = "docker.volume"
)

// SessionBackend cleans up Docker resources recorded in the session ledger.
//
// Ownership is proven from Docker, not from the ledger: a container is only
// claimed when its codefly.invocation label matches the recorded invocation and
// its creation instant matches the recorded witness. That is what makes the
// adapter safe against the two ways a name-based sweep goes wrong — a container
// recreated under the same name, and a developer's own container that happens
// to be named like ours.
//
// A container reused by name keeps the label of the invocation that created it.
// A later session must therefore declare it Borrowed, which is what stops that
// session from taking down warm state it did not create.
type SessionBackend struct {
	client client.APIClient
}

// NewSessionBackend connects to the local Docker engine. It returns an error
// when the engine is unreachable; a caller that treats Docker as optional
// should skip registering the adapter rather than register a broken one — a
// missing adapter is recorded as "refused", which preserves state, whereas a
// failing one would look like a cleanup failure.
func NewSessionBackend(ctx context.Context) (*SessionBackend, error) {
	cli, _, err := newDockerClient()
	if err != nil {
		return nil, fmt.Errorf("cannot create docker client: %w", err)
	}
	if err := pingDockerClient(ctx, cli); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker engine is not reachable: %w", err)
	}
	return &SessionBackend{client: cli}, nil
}

// Close releases the Docker client.
func (backend *SessionBackend) Close() error {
	return backend.client.Close()
}

// ContainerResource describes a container for the ledger. A stopped container
// still exists and still holds its writable layer, so it never vanishes on stop
// and its record is kept until something deletes it.
func ContainerResource(name string, ownership sessionledger.Ownership, data bool) sessionledger.Resource {
	return sessionledger.Resource{
		Kind:      KindContainer,
		Backend:   BackendDocker,
		ID:        ContainerName(name),
		Ownership: ownership,
		Data:      data,
	}
}

// VolumeResource describes a named volume for the ledger. A volume is always
// data, so a stop never removes one.
func VolumeResource(name string, ownership sessionledger.Ownership) sessionledger.Resource {
	return sessionledger.Resource{
		Kind:      KindVolume,
		Backend:   BackendDocker,
		ID:        name,
		Ownership: ownership,
		Data:      true,
	}
}

// ContainerWitness reads the identity that proves which incarnation of a
// container name is the one being recorded.
func (backend *SessionBackend) ContainerWitness(ctx context.Context, name string) (sessionledger.Witness, error) {
	inspect, err := backend.client.ContainerInspect(ctx, ContainerName(name))
	if err != nil {
		if errdefs.IsNotFound(err) {
			return sessionledger.Witness{}, sessionledger.ErrNotFound
		}
		return sessionledger.Witness{}, fmt.Errorf("cannot inspect container %s: %w", name, err)
	}
	created, err := time.Parse(time.RFC3339Nano, inspect.Created)
	if err != nil {
		return sessionledger.Witness{}, fmt.Errorf("cannot parse container creation time: %w", err)
	}
	created = created.UTC()
	return sessionledger.Witness{CreatedAt: &created}, nil
}

// Claim proves the resource exists and carries this invocation's label.
func (backend *SessionBackend) Claim(
	ctx context.Context,
	resource sessionledger.Resource,
	invocation string,
) (sessionledger.Observation, error) {
	switch resource.Kind {
	case KindContainer:
		return backend.claimContainer(ctx, resource, invocation)
	case KindVolume:
		return backend.claimVolume(ctx, resource, invocation)
	default:
		return sessionledger.Observation{}, fmt.Errorf("docker backend does not own kind %q", resource.Kind)
	}
}

func (backend *SessionBackend) claimContainer(
	ctx context.Context,
	resource sessionledger.Resource,
	invocation string,
) (sessionledger.Observation, error) {
	inspect, err := backend.client.ContainerInspect(ctx, resource.ID)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return sessionledger.Observation{}, sessionledger.ErrNotFound
		}
		return sessionledger.Observation{}, fmt.Errorf("cannot inspect container %s: %w", resource.ID, err)
	}
	if labelOf(inspect.Config, LabelCodeflyInvocation) != invocation {
		return sessionledger.Observation{}, sessionledger.ErrNotOwned
	}
	created, err := time.Parse(time.RFC3339Nano, inspect.Created)
	if err != nil {
		return sessionledger.Observation{}, fmt.Errorf("cannot parse container creation time: %w", err)
	}
	created = created.UTC()
	return sessionledger.Observation{
		Live:    inspect.State != nil && inspect.State.Running,
		Witness: sessionledger.Witness{CreatedAt: &created},
	}, nil
}

func (backend *SessionBackend) claimVolume(
	ctx context.Context,
	resource sessionledger.Resource,
	invocation string,
) (sessionledger.Observation, error) {
	volume, err := backend.client.VolumeInspect(ctx, resource.ID)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return sessionledger.Observation{}, sessionledger.ErrNotFound
		}
		return sessionledger.Observation{}, fmt.Errorf("cannot inspect volume %s: %w", resource.ID, err)
	}
	if volume.Labels[LabelCodeflyInvocation] != invocation {
		return sessionledger.Observation{}, sessionledger.ErrNotOwned
	}
	created, err := time.Parse(time.RFC3339Nano, volume.CreatedAt)
	if err != nil {
		// An engine that reports a creation time this adapter cannot read
		// leaves it with no witness to offer. Reconciliation then compares a
		// recorded witness against nothing and preserves the volume, which is
		// the right answer for state whose identity cannot be established.
		return sessionledger.Observation{}, nil
	}
	created = created.UTC()
	// A volume never executes, so it is present but never live.
	return sessionledger.Observation{Witness: sessionledger.Witness{CreatedAt: &created}}, nil
}

// Stop ends container execution and keeps everything the container holds.
//
// A volume never executes, so reconciliation never asks — a volume's Claim
// reports it as not live and the stop is skipped. Any other kind reaching here
// is a resource this adapter does not own, and reporting success for something
// it did not touch is how a resource ends up recorded as stopped while it is
// still running.
func (backend *SessionBackend) Stop(ctx context.Context, resource sessionledger.Resource) error {
	if resource.Kind == KindVolume {
		return nil
	}
	if resource.Kind != KindContainer {
		return fmt.Errorf("docker backend does not own kind %q", resource.Kind)
	}
	err := backend.client.ContainerStop(ctx, resource.ID, container.StopOptions{})
	if errdefs.IsNotFound(err) {
		return sessionledger.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("cannot stop container %s: %w", resource.ID, err)
	}
	return nil
}

// Delete removes the resource. For a container this also removes the anonymous
// volumes Docker created for it; named volumes are separate ledger resources
// and are only removed when the ledger recorded them as owned.
func (backend *SessionBackend) Delete(ctx context.Context, resource sessionledger.Resource) error {
	switch resource.Kind {
	case KindContainer:
		err := backend.client.ContainerRemove(ctx, resource.ID, container.RemoveOptions{
			Force:         true,
			RemoveVolumes: true,
		})
		if errdefs.IsNotFound(err) {
			return sessionledger.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("cannot remove container %s: %w", resource.ID, err)
		}
		return nil
	case KindVolume:
		err := backend.client.VolumeRemove(ctx, resource.ID, true)
		if errdefs.IsNotFound(err) {
			return sessionledger.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("cannot remove volume %s: %w", resource.ID, err)
		}
		return nil
	default:
		return fmt.Errorf("docker backend does not own kind %q", resource.Kind)
	}
}

func labelOf(config *container.Config, label string) string {
	if config == nil {
		return ""
	}
	return config.Labels[label]
}

var _ sessionledger.Backend = (*SessionBackend)(nil)
