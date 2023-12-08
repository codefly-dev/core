package configurations

import (
	"fmt"
	"path/filepath"
	"strings"
)

/*
References help find where the resource is located

Convention: relativePath is the name unless specified otherwise

*/

func Pointer[T any](t T) *T {
	return &t
}

// OverridePath is nil if the name is the same as the desired relative path
func OverridePath(name string, path string) *string {
	if path == "" || path == name {
		return nil
	}
	if filepath.IsAbs(path) {
		return Pointer(path)
	}
	return Pointer(path)
}

func ReferenceMatch(entry string, name string) bool {
	return entry == name || entry == fmt.Sprintf("%s*", name)
}

func MakeActive(entry string) string {
	if strings.HasSuffix(entry, "*") {
		return entry
	}
	return fmt.Sprintf("%s*", entry)
}

func MakeInactive(entry string) string {
	if name, ok := strings.CutSuffix(entry, "*"); ok {
		return name
	}
	return entry
}

//
//func (ref *ProjectReference) OverridePath() string {
//	if ref.PathOverride != nil {
//		return *ref.PathOverride
//	}
//	return ref.Name
//}
//
//func (ref *ProjectReference) WithRelativePath(relativePath string) *ProjectReference {
//	if ref.Name != relativePath {
//		ref.PathOverride = Pointer(relativePath)
//	}
//	return ref
//}

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

func (ref *ServiceReference) String() string {
	return fmt.Sprintf("%s/%s", ref.Application, ref.Name)
}

func ParseServiceReference(input string) (*ServiceReference, error) {
	parts := strings.Split(input, "/")
	switch len(parts) {
	case 1:
		return &ServiceReference{Name: parts[0]}, nil
	case 2:
		return &ServiceReference{Name: parts[1], Application: parts[0]}, nil
	default:
		return nil, fmt.Errorf("invalid service input: %s", input)
	}
}

// Projects reference Applications

// Projects reference Providers

// A ProviderReference
type ProviderReference struct {
	Name                 string  `yaml:"name"`
	RelativePathOverride *string `yaml:"relative-path,omitempty"`
}

// Services reference Endpoints

// An EndpointReference
type EndpointReference struct {
	Name string `yaml:"name"`
}

// Projects reference Environments
