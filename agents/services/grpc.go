package services

// gRPC embed types for service plugins.
//
// Plugins embed these instead of importing proto-generated Unimplemented*
// types directly. This keeps all gRPC infrastructure in core.

import (
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

// RuntimeServer is embedded by plugin runtime types to satisfy the gRPC
// RuntimeServer interface. Plugins override methods they implement.
type RuntimeServer struct {
	runtimev0.UnimplementedRuntimeServer
}

// BuilderServer is embedded by plugin builder types to satisfy the gRPC
// BuilderServer interface. Plugins override methods they implement.
type BuilderServer struct {
	builderv0.UnimplementedBuilderServer
}

// CodeServer is embedded by plugin code types to satisfy the gRPC
// CodeServer interface. Plugins override methods they implement.
type CodeServer struct {
	codev0.UnimplementedCodeServer
}
