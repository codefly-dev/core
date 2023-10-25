package configurations

import (
	"fmt"
	"github.com/codefly-dev/core/shared"
)

// Workspace references Projects

// ProjectReference is a reference to a project used by Workspace configuration
type ProjectReference struct {
	Name         string `yaml:"name"`
	RelativePath string `yaml:"relative-path,omitempty"`
}

// Application reference services

// ServiceReference is a reference to a service used by Application configuration
type ServiceReference struct {
	Name                string `yaml:"name"`
	RelativePath        string `yaml:"relative-path,omitempty"`
	ApplicationOverride string `yaml:"applications,omitempty"`

	RunningOptions RunningOptions `yaml:"options,omitempty"`
}

// RunningOptions of the ServiceReference can tweak running behavior of service
// Note: this is not a part of the Service configuration but part of the Application running
type RunningOptions struct {
	Replicas    int  `yaml:"replicas,omitempty"`
	Quiet       bool `yaml:"quiet,omitempty"`
	Persistence bool `yaml:"persistence,omitempty"`
}

func (ref *ServiceReference) Validate() error {
	return nil
}

func (ref *ServiceReference) CreateReplicas() []string {
	logger := shared.NewLogger("configurations.ServiceRunner.Replicas")
	if ref.RunningOptions.Replicas == 0 {
		return nil
	}
	logger.Debugf("creating replica services - useful for logging")
	var names []string
	for i := 0; i < ref.RunningOptions.Replicas; i++ {
		names = append(names, fmt.Sprintf("%s-%d", ref.Name, i+1))
	}
	return names
}

// Projects reference Applications

// An ApplicationReference
type ApplicationReference struct {
	Name         string `yaml:"name"`
	RelativePath string `yaml:"relative-path,omitempty"`
}

// Projects reference Providers

// A ProviderReference
type ProviderReference struct {
	Name         string `yaml:"name"`
	RelativePath string `yaml:"relative-path,omitempty"`
}
