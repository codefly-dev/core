package resources

import (
	"fmt"
	"os"
	"slices"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

const (
	RuntimeContextNative    = "native"
	RuntimeContextNix       = "nix"
	RuntimeContextContainer = "container"
	RuntimeContextFree      = "free"

	NetworkAccessContainer = "container"
	NetworkAccessNative    = "native"
	NetworkAccessPublic    = "public"
)

func RuntimeContexts() []string {
	return []string{RuntimeContextNative, RuntimeContextNix, RuntimeContextContainer, RuntimeContextFree}
}

func NewRuntimeContext(runtimeContext string) (*basev0.RuntimeContext, error) {
	switch runtimeContext {
	case RuntimeContextNative:
		return NewRuntimeContextNative(), nil
	case RuntimeContextNix:
		return NewRuntimeContextNix(), nil
	case RuntimeContextContainer:
		return NewRuntimeContextContainer(), nil
	case RuntimeContextFree:
		return NewRuntimeContextFree(), nil
	default:
		return nil, fmt.Errorf("unknown runtime context: %s", runtimeContext)
	}
}

// RuntimeContextFromInstance returns a runtime context from a network instance.
func RuntimeContextFromInstance(instance *basev0.NetworkInstance) *basev0.RuntimeContext {
	switch instance.Access.Kind {
	case NetworkAccessNative:
		return NewRuntimeContextNative()
	case NetworkAccessContainer:
		return NewRuntimeContextContainer()
	default:
		return NewRuntimeContextFree()
	}
}

// RuntimeContextFromEnv returns a runtime context from the environment variable.
func RuntimeContextFromEnv() *basev0.RuntimeContext {
	env := os.Getenv("CODEFLY__RUNTIME_CONTEXT")
	switch env {
	case RuntimeContextNative:
		return NewRuntimeContextNative()
	case RuntimeContextNix:
		return NewRuntimeContextNix()
	case RuntimeContextContainer:
		return NewRuntimeContextContainer()
	default:
		return NewRuntimeContextNative()
	}
}

// NetworkAccessFromRuntimeContext returns a NetworkAccess from a runtime context.
// Both native and nix run on the host, so they map to native network access.
func NetworkAccessFromRuntimeContext(runtimeContext *basev0.RuntimeContext) *basev0.NetworkAccess {
	switch runtimeContext.Kind {
	case RuntimeContextContainer:
		return NewContainerNetworkAccess()
	default:
		return NewNativeNetworkAccess()
	}
}

func NewRuntimeContextNative() *basev0.RuntimeContext {
	return &basev0.RuntimeContext{Kind: RuntimeContextNative}
}

func NewRuntimeContextNix() *basev0.RuntimeContext {
	return &basev0.RuntimeContext{Kind: RuntimeContextNix}
}

func NewRuntimeContextContainer() *basev0.RuntimeContext {
	return &basev0.RuntimeContext{Kind: RuntimeContextContainer}
}

func NewRuntimeContextFree() *basev0.RuntimeContext {
	return &basev0.RuntimeContext{Kind: RuntimeContextFree}
}

func ValidateRuntimeContext(rc string) error {
	if !slices.Contains(RuntimeContexts(), rc) {
		return fmt.Errorf("invalid runtime context: %s", rc)
	}
	return nil
}
