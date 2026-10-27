package dockerrun

import (
	"fmt"
	"strings"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/resources/names"
)

// LabelCodeflyService records the exact parent module/service. Workspace and
// host ownership are supplied separately by the recovery scope and namespace.
const LabelCodeflyService = "codefly.service"

// WithServiceIdentity snapshots a container's parent service before first use.
// Version is deliberately excluded: a service upgrade may reuse its containers.
// An agent passes its loaded Base.Identity to each of its Docker environments.
func (docker *DockerEnvironment) WithServiceIdentity(identity *resources.ServiceIdentity) error {
	key, err := containerServiceKey(identity)
	if err != nil {
		return err
	}
	docker.mu.Lock()
	defer docker.mu.Unlock()
	if docker.recoveryScope != nil {
		return fmt.Errorf("container service identity must be bound before first use")
	}
	docker.serviceIdentity = key
	return nil
}

func containerServiceKey(identity *resources.ServiceIdentity) (string, error) {
	if identity == nil || !names.IsModule(identity.Module) ||
		!names.IsPathPrefix(identity.Name) || strings.Contains(identity.Name, "/") {
		return "", fmt.Errorf("container service identity requires a valid module and a single service name")
	}
	return identity.Unique(), nil
}

// ContainerServiceIdentity reads exact parent attribution from Docker labels.
// A missing label returns nil, nil: it proves no parent, regardless of the
// container name. A present but malformed label returns an error. The returned
// identity contains only Module and Name; callers must separately check scope,
// namespace, concrete name, lifecycle and any resource-specific constraints.
func ContainerServiceIdentity(labels map[string]string) (*resources.ServiceIdentity, error) {
	key, present := labels[LabelCodeflyService]
	if !present {
		return nil, nil
	}
	module, name, ok := strings.Cut(key, "/")
	identity := &resources.ServiceIdentity{Module: module, Name: name}
	if _, err := containerServiceKey(identity); !ok || err != nil {
		return nil, fmt.Errorf("container has an invalid %s label", LabelCodeflyService)
	}
	return identity, nil
}

func validateContainerServiceReuse(labels map[string]string, expected string) error {
	identity, err := ContainerServiceIdentity(labels)
	if err != nil {
		return err
	}
	// Legacy containers remain eligible for the existing exact-name reuse
	// checks. Absence is never positive parent attribution, and this does not
	// relabel or replace a container merely to obtain attribution.
	if identity != nil && identity.Unique() != expected {
		return fmt.Errorf("container belongs to another or unbound parent service; refusing adoption or replacement")
	}
	return nil
}
