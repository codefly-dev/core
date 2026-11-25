package python

import (
	"os/exec"

	basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"
	"github.com/codefly-dev/core/resources"
)

// SetPythonRuntimeContext determines the runtime context based on the
// requested context and available Python toolchain (uv).
func SetPythonRuntimeContext(runtimeContext *basev1.RuntimeContext) *basev1.RuntimeContext {
	if runtimeContext.Kind == resources.RuntimeContextNix {
		return resources.NewRuntimeContextNix()
	}
	if runtimeContext.Kind == resources.RuntimeContextFree || runtimeContext.Kind == resources.RuntimeContextNative {
		if HasUVRuntime() {
			return resources.NewRuntimeContextNative()
		}
	}
	return resources.NewRuntimeContextContainer()
}

// HasUVRuntime checks if the uv Python package manager is available.
func HasUVRuntime() bool {
	_, err := exec.LookPath("uv")
	return err == nil
}
