package configurations

import (
	"fmt"
	"path/filepath"
	"strings"

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

// Application reference services

// RunningOptions of the ServiceReference can tweak running behavior of service
// Note: this is not a part of the Service configuration but part of the Application running
type RunningOptions struct {
	Quiet       bool `yaml:"quiet,omitempty"`
	Persistence bool `yaml:"persistence,omitempty"`
}

// Projects reference Applications

// Services reference Endpoints

// An EndpointReference
type EndpointReference struct {
	Name string `yaml:"name"`
}

// Projects reference Environments
