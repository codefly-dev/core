package resources

import (
	"fmt"
	"slices"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

const (
	RuntimeContextNative    = "native"
	RuntimeContextContainer = "container"
	RuntimeContextFree      = "free"

	NetworkAccessContainer = "container"
	NetworkAccessNative    = "native"
	NetworkAccessPublic    = "public"
)

func RuntimeContexts() []string {
	return []string{RuntimeContextContainer, RuntimeContextNative, RuntimeContextFree}
}

func NewRuntimeContext(runtimeContext string) (*basev0.RuntimeContext, error) {
	switch runtimeContext {
	case RuntimeContextContainer:
		return NewRuntimeContextContainer(), nil
	case RuntimeContextNative:
		return NewRuntimeContextNative(), nil
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

// NetworkAccessFromRuntimeContext returns a NetworkAccess from a runtime instance
func NetworkAccessFromRuntimeContext(runtimeContext *basev0.RuntimeContext) *basev0.NetworkAccess {
	switch runtimeContext.Kind {
	case RuntimeContextContainer:
		return NewContainerNetworkAccess()
	default:
		return NewNativeNetworkAccess()
	}
}

func NewRuntimeContextContainer() *basev0.RuntimeContext {
	return &basev0.RuntimeContext{Kind: RuntimeContextContainer}
}

func NewRuntimeContextNative() *basev0.RuntimeContext {
	return &basev0.RuntimeContext{Kind: RuntimeContextNative}
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
