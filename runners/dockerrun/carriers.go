package dockerrun

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
)

// carrierRoot is where a container's file-delivered values are written on
// the host: one directory per container name, so an unchanged declaration
// mounts the same source and keeps its reuse fingerprint.
func (docker *DockerEnvironment) carrierRoot() string {
	return filepath.Join(os.TempDir(), "codefly-carriers", docker.name)
}

// deliverCarriers writes the container's file-delivered values
// (resources.MaterializeFileCarriers) into its carrier directory, mounts that
// directory read-only at resources.ContainerFileCarrierMount, and sets the
// container environment to the paths as the container sees them. An
// environment the platform would refuse is refused here, before the container
// is created. A container with no file-delivered value gets no mount, so its
// declaration is exactly what it was before file delivery existed.
func (docker *DockerEnvironment) deliverCarriers(config *container.Config, host *container.HostConfig) error {
	docker.mu.Lock()
	envs := append([]*resources.EnvironmentVariable(nil), docker.envs...)
	docker.mu.Unlock()
	file := false
	for _, env := range envs {
		file = file || (env != nil && env.File)
	}
	if file {
		dir := docker.carrierRoot()
		// Files are named by content; anything left from an earlier
		// declaration is not this one's.
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("cannot reset %s: %w", dir, err)
		}
		materialized, err := resources.MaterializeFileCarriers(dir, resources.ContainerFileCarrierMount, envs)
		if err != nil {
			return err
		}
		envs = materialized
		host.Mounts = append(host.Mounts, mount.Mount{Type: mount.TypeBind, Source: dir, Target: resources.ContainerFileCarrierMount, ReadOnly: true})
		docker.mu.Lock()
		docker.carrierDir = dir
		docker.mu.Unlock()
	}
	if err := resources.CheckProcessEnvironment(envs); err != nil {
		return err
	}
	config.Env = resources.EnvironmentVariableAsStrings(envs)
	return nil
}

// execCarriers delivers an exec's file-delivered values through the
// container's carrier mount. A container created without one cannot see a
// file written now, so such a value is refused rather than inlined.
func (docker *DockerEnvironment) execCarriers(envs []*resources.EnvironmentVariable) ([]*resources.EnvironmentVariable, error) {
	file := false
	for _, env := range envs {
		file = file || (env != nil && env.File)
	}
	if file {
		docker.mu.Lock()
		dir := docker.carrierDir
		docker.mu.Unlock()
		if dir == "" {
			return nil, fmt.Errorf("%w: the container was created without a file carrier mount, so a value delivered by file at exec cannot reach it", resources.ErrFileCarrier)
		}
		materialized, err := resources.MaterializeFileCarriers(dir, resources.ContainerFileCarrierMount, envs)
		if err != nil {
			return nil, err
		}
		envs = materialized
	}
	if err := resources.CheckProcessEnvironment(envs); err != nil {
		return nil, err
	}
	return envs, nil
}

// releaseCarriers removes the container's carrier directory once the
// container is gone.
func (docker *DockerEnvironment) releaseCarriers() {
	docker.mu.Lock()
	dir := docker.carrierDir
	docker.carrierDir = ""
	docker.mu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}
