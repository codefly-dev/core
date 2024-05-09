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

// s reference Modules

// ServiceReferences reference Endpoints

// An EndpointReference
type EndpointReference struct {
	Name string `yaml:"name"`
}

// s reference Environments
