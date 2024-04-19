package resources

import basev0 "github.com/codefly-dev/core/generated/go/base/v0"

// RuntimeContextFromInstance returns a runtime context from a network instance.
func RuntimeContextFromInstance(instance *basev0.NetworkInstance) *basev0.RuntimeContext {
	switch instance.Access.Kind {
	case basev0.NetworkAccess_FromNative:
		return RuntimeContextNative()
	case basev0.NetworkAccess_FromContainer:
		return RuntimeContextContainer()
	default:
		return RuntimeContextFree()
	}
}

// NetworkAccessFromRuntimeContext returns a NetworkAccess from a runtime instance
func NetworkAccessFromRuntimeContext(runtimeContext *basev0.RuntimeContext) *basev0.NetworkAccess {
	switch runtimeContext.Kind {
	case basev0.RuntimeContext_Container:
		return ContainerNetworkAccess()
	default:
		return NativeNetworkAccess()
	}
}

// ContainerRuntimeContext returns a new container runtime context.
func RuntimeContextContainer() *basev0.RuntimeContext {
	return &basev0.RuntimeContext{Kind: basev0.RuntimeContext_Container}
}

// NativeRuntimeContext returns a new native runtime context.
func RuntimeContextNative() *basev0.RuntimeContext {
	return &basev0.RuntimeContext{Kind: basev0.RuntimeContext_Native}
}

// FreeRuntimeContext returns a new free runtime context.
func RuntimeContextFree() *basev0.RuntimeContext {
	return &basev0.RuntimeContext{Kind: basev0.RuntimeContext_Free}
}
