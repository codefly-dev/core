package services

// gRPC embed types for service plugins.
//
// Plugins embed these instead of importing proto-generated Unimplemented*
// types directly. This keeps all gRPC infrastructure in core.

import (
	"context"
	"errors"

	executionv1 "github.com/codefly-dev/core/generated/go/codefly/execution/v1"
	builderv1 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v1"
	codev1 "github.com/codefly-dev/core/generated/go/codefly/services/code/v1"
	runtimev1 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v1"
)

// RuntimeServer is embedded by plugin runtime types to satisfy the gRPC
// RuntimeServer interface. Plugins override methods they implement.
type RuntimeServer struct {
	runtimev1.UnimplementedRuntimeServer
}

// BuilderServer is embedded by plugin builder types to satisfy the gRPC
// BuilderServer interface. Plugins override methods they implement.
type BuilderServer struct {
	builderv1.UnimplementedBuilderServer
}

// CodeServer is embedded by plugin code types to satisfy the gRPC
// CodeServer interface. Plugins override methods they implement.
type CodeServer struct {
	codev1.UnimplementedCodeServer
}

// ExecutionExporterServer is embedded by execution receipt exporter plugins.
type ExecutionExporterServer struct {
	executionv1.UnimplementedExecutionExporterServer
}

// DefaultBuilder supplies the successful no-op lifecycle methods used by most
// plugins. Embed it instead of BuilderServer and implement only the operations
// the plugin actually specializes (usually Load, Create, Deploy, and optionally
// Build). Explicit plugin methods always take precedence over these promoted
// defaults.
type DefaultBuilder struct {
	BuilderServer
	wrapper *BuilderWrapper
}

var errDefaultBuilderNotWired = errors.New("default builder is not wired")

func NewDefaultBuilder(wrapper *BuilderWrapper) *DefaultBuilder {
	return &DefaultBuilder{wrapper: wrapper}
}

func (s *DefaultBuilder) Init(_ context.Context, request *builderv1.InitRequest) (*builderv1.InitResponse, error) {
	if s == nil || s.wrapper == nil {
		return (&BuilderWrapper{}).InitError(errDefaultBuilderNotWired)
	}
	s.wrapper.DependencyEndpoints = request.GetDependenciesEndpoints()
	return s.wrapper.InitResponse()
}

func (s *DefaultBuilder) Update(context.Context, *builderv1.UpdateRequest) (*builderv1.UpdateResponse, error) {
	if s == nil || s.wrapper == nil {
		return (&BuilderWrapper{}).UpdateError(errDefaultBuilderNotWired)
	}
	return s.wrapper.UpdateResponse()
}

func (s *DefaultBuilder) Sync(_ context.Context, request *builderv1.SyncRequest) (*builderv1.SyncResponse, error) {
	if s == nil || s.wrapper == nil {
		return (&BuilderWrapper{}).SyncError(errDefaultBuilderNotWired)
	}
	if request.GetDryRun() {
		return s.wrapper.SyncUnsupported("this service agent does not provide non-mutating sync drift detection")
	}
	return s.wrapper.SyncResponse()
}

func (s *DefaultBuilder) Build(context.Context, *builderv1.BuildRequest) (*builderv1.BuildResponse, error) {
	if s == nil || s.wrapper == nil {
		return (&BuilderWrapper{}).BuildError(errDefaultBuilderNotWired)
	}
	return s.wrapper.BuildResponse()
}

func (s *DefaultBuilder) Audit(context.Context, *builderv1.AuditRequest) (*builderv1.AuditResponse, error) {
	if s == nil || s.wrapper == nil {
		return (&BuilderWrapper{}).AuditUnsupported(errDefaultBuilderNotWired.Error())
	}
	return s.wrapper.AuditUnsupported("this service agent does not provide an authoritative vulnerability audit")
}

func (s *DefaultBuilder) SBOM(context.Context, *builderv1.SBOMRequest) (*builderv1.SBOMResponse, error) {
	if s == nil || s.wrapper == nil {
		return (&BuilderWrapper{}).SBOMUnsupported(errDefaultBuilderNotWired.Error())
	}
	return s.wrapper.SBOMUnsupported("this service agent does not provide an authoritative SBOM")
}

func (s *DefaultBuilder) Package(context.Context, *builderv1.PackageRequest) (*builderv1.PackageResponse, error) {
	if s == nil || s.wrapper == nil {
		return (&BuilderWrapper{}).PackageUnsupported(errDefaultBuilderNotWired.Error())
	}
	return s.wrapper.PackageUnsupported("this service agent does not provide portable source packaging")
}

// Communicate is an empty question stream by default. Plugins with creation or
// synchronization questions override it with their own implementation.
func (s *DefaultBuilder) Communicate(builderv1.Builder_CommunicateServer) error {
	return nil
}

// DefaultRuntime supplies lifecycle methods that are universally safe to
// share. Process-owning operations such as Start, Stop, Destroy, and Test stay
// explicit so a plugin cannot accidentally skip cleanup or claim a false test
// success.
type DefaultRuntime struct {
	RuntimeServer
	wrapper *RuntimeWrapper
}

func NewDefaultRuntime(wrapper *RuntimeWrapper) *DefaultRuntime {
	return &DefaultRuntime{wrapper: wrapper}
}

func (s *DefaultRuntime) Information(ctx context.Context, req *runtimev1.InformationRequest) (*runtimev1.InformationResponse, error) {
	if s == nil || s.wrapper == nil {
		return (&RuntimeWrapper{}).InformationResponse(ctx, req)
	}
	return s.wrapper.InformationResponse(ctx, req)
}
