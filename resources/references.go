package resources

import (
	"path/filepath"

	"github.com/codefly-dev/core/shared"
)

/*
References help find where the resource is located

Convention: relativePath is the name unless specified otherwise

*/

// OverridePath is nil if the name is the same as the desired relative path
func OverridePath(defaultPath string, path string) *string {
	if path == "" || path == defaultPath {
		return nil
	}
	if filepath.IsAbs(path) {
		return shared.Pointer(path)
	}
	return shared.Pointer(path)
}

func ReferenceMatch(entry string, name string) bool {
	return entry == name
}

// Module reference services

// RunningOptions of the ServiceReference can tweak running behavior of service
// Note: this is not a part of the Service configuration but part of the Module running
type RunningOptions struct {
	Quiet       bool `yaml:"quiet,omitempty"`
	Persistence bool `yaml:"persistence,omitempty"`
}

// ServiceReferences reference Endpoints

// An EndpointReference
type EndpointReference struct {
	API  string `yaml:"api"`
	Name string `yaml:"name"`

	// Required says whether the consumer's readiness waits on this endpoint.
	// Unset means required: consuming an endpoint without needing it to work is
	// the exception, and it has to be written down. Setting it to false is how a
	// consumer opts out of an endpoint it only reaches opportunistically.
	Required *bool `yaml:"required,omitempty"`
}

// IsRequired reports whether readiness waits on this endpoint.
func (e *EndpointReference) IsRequired() bool {
	return e.Required == nil || *e.Required
}

func (e *EndpointReference) GetAPI() string {
	if e.API != "" {
		return e.API
	}
	return e.Name
}
