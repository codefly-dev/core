package configurations

import (
	"fmt"
	"path"
)

/*
References help find where the resource is located

Convention: relativePath is the name unless specified otherwise

*/

func Pointer[T any](t T) *T {
	return &t
}

// RelativePath is nil if the name is the same as the desired relative path
func RelativePath(name string, rel string) *string {
	if rel == name {
		return nil
	}
	return Pointer(rel)
}

// Workspace references Projects

// ProjectReference is a reference to a project used by Workspace configuration
type ProjectReference struct {
	Name                 string  `yaml:"name"`
	RelativePathOverride *string `yaml:"relative-path,omitempty"`
}

func (ref *ProjectReference) RelativePath() string {
	if ref.RelativePathOverride != nil {
		return *ref.RelativePathOverride
	}
	return ref.Name
}

func (ref *ProjectReference) WithRelativePath(relativePath string) *ProjectReference {
	if ref.Name != relativePath {
		ref.RelativePathOverride = Pointer(relativePath)
	}
	return ref
}

// Application reference services

// RunningOptions of the ServiceReference can tweak running behavior of service
// Note: this is not a part of the Service configuration but part of the Application running
type RunningOptions struct {
	Quiet       bool `yaml:"quiet,omitempty"`
	Persistence bool `yaml:"persistence,omitempty"`
}

// ServiceReference is a reference to a service used by Application configuration
type ServiceReference struct {
	Name                 string  `yaml:"name"`
	RelativePathOverride *string `yaml:"relative-path,omitempty"`
	Application          string  `yaml:"application,omitempty"`

	RunningOptions RunningOptions `yaml:"options,omitempty"`
}

func (ref *ServiceReference) RelativePath() string {
	if ref.RelativePathOverride != nil {
		return *ref.RelativePathOverride
	}
	return ref.Name
}

func (ref *ServiceReference) Dir(opts ...Option) (string, error) {
	scope := WithScope(opts...)
	return path.Join(scope.Application.Dir(), ref.RelativePath()), nil
}

func (ref *ServiceReference) String() string {
	return fmt.Sprintf("%s/%s", ref.Application, ref.Name)
}

// Projects reference Applications

// An ApplicationReference
type ApplicationReference struct {
	Name                 string  `yaml:"name"`
	RelativePathOverride *string `yaml:"relative-path,omitempty"`
}

func (r ApplicationReference) RelativePath() string {
	if r.RelativePathOverride != nil {
		return *r.RelativePathOverride
	}
	return r.Name
}

// Projects reference Providers

// A ProviderReference
type ProviderReference struct {
	Name                 string  `yaml:"name"`
	RelativePathOverride *string `yaml:"relative-path,omitempty"`
}

// Services reference Endpoints

// A EndpointReference
type EndpointReference struct {
	Name string `yaml:"name"`
}
