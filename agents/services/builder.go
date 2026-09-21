package services

import (
	"context"
	"fmt"
	"path/filepath"

	dockerhelpers "github.com/codefly-dev/core/agents/helpers/docker"
	"github.com/codefly-dev/core/agents/manager"
	"github.com/codefly-dev/core/artifactexecution"
	"github.com/codefly-dev/core/resources"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"google.golang.org/grpc"
)

// BuilderAgent is the client-side wrapper for the Builder gRPC service.
type BuilderAgent struct {
	builderv0.BuilderClient
	Agent       *resources.Agent
	ProcessInfo *manager.ProcessInfo
}

// NewBuilderAgentClient creates a BuilderAgent from an existing gRPC connection.
func NewBuilderAgentClient(conn *grpc.ClientConn) *BuilderAgent {
	return &BuilderAgent{
		BuilderClient: builderv0.NewBuilderClient(conn),
	}
}

// Build checks selection support before dispatch, then requires acknowledgement
// when the agent executes the image build. Recipe execution belongs to the caller.
func (b *BuilderAgent) Build(ctx context.Context, req *builderv0.BuildRequest, opts ...grpc.CallOption) (*builderv0.BuildResponse, error) {
	if req.GetExecution() != nil && !filepath.IsAbs(req.GetOutputDirectory()) {
		return nil, fmt.Errorf("bound build requires an absolute staging directory")
	}
	if err := b.checkExecution(ctx, req.GetExecution(), artifactexecution.BuilderBuild, opts...); err != nil {
		return nil, err
	}
	cache := req.GetBuildContext().GetDockerBuildContext().GetCache()
	if cache != nil {
		if _, err := dockerhelpers.CacheArguments(cache, RecipeBuildPlatforms()); err != nil {
			return nil, err
		}
	}
	selected := req.GetBuildContext().GetDockerBuildContext().GetBuildxBuilder()
	if selected != "" {
		capabilities, err := b.BuildCapabilities(ctx, &builderv0.BuildCapabilitiesRequest{}, opts...)
		if err != nil {
			return nil, fmt.Errorf("cannot verify builder agent support for requested Buildx builder %q before execution; upgrade the agent: %w", selected, err)
		}
		if !capabilities.GetBuildxSelection() {
			return nil, fmt.Errorf("builder agent does not support Buildx selection; refusing to execute with requested builder %q; upgrade the agent", selected)
		}
	}
	resp, err := b.BuilderClient.Build(ctx, req, opts...)
	if err == nil && cache != nil && resp.GetState().GetState() == builderv0.BuildStatus_SUCCESS && resp.GetResult().GetDockerBuildPlan() == nil && resp.GetCacheContractVersion() != dockerhelpers.CacheContractVersion {
		return nil, fmt.Errorf("builder agent did not acknowledge build cache contract %s; upgrade the agent or remove cache options", dockerhelpers.CacheContractVersion)
	}
	if selected != "" && err == nil && resp.GetState().GetState() == builderv0.BuildStatus_SUCCESS && resp.GetResult().GetDockerBuildPlan() == nil && resp.GetBuildxBuilder() != selected {
		return nil, fmt.Errorf("builder agent did not acknowledge requested Buildx builder %q; upgrade the agent", selected)
	}
	if err == nil && req.GetExecution() != nil {
		if resp.GetState().GetState() != builderv0.BuildStatus_SUCCESS {
			return nil, fmt.Errorf("bound build did not succeed: %s", resp.GetState().GetMessage())
		}
		if err := artifactexecution.CheckReceipt(req.GetExecution(), resp.GetExecution()); err != nil {
			return nil, err
		}
	}
	return resp, err
}

func (b *BuilderAgent) checkExecution(ctx context.Context, execution *basev0.ArtifactExecution, protocol string, opts ...grpc.CallOption) error {
	if execution == nil {
		return nil
	}
	if b.ProcessInfo == nil {
		return fmt.Errorf("verified executor process identity is required")
	}
	capabilities, err := b.BuildCapabilities(ctx, &builderv0.BuildCapabilitiesRequest{}, opts...)
	if err != nil {
		return fmt.Errorf("cannot verify artifact execution support before dispatch: %w", err)
	}
	return artifactexecution.Check(execution, protocol, b.ProcessInfo.ArtifactDigest, capabilities.GetExecutionContracts())
}

func (b *BuilderAgent) Deploy(ctx context.Context, req *builderv0.DeploymentRequest, opts ...grpc.CallOption) (*builderv0.DeploymentResponse, error) {
	if req.GetExecution() != nil && !filepath.IsAbs(req.GetOutputDirectory()) {
		return nil, fmt.Errorf("bound render requires an absolute staging directory")
	}
	if err := b.checkExecution(ctx, req.GetExecution(), artifactexecution.BuilderRender, opts...); err != nil {
		return nil, err
	}
	response, err := b.BuilderClient.Deploy(ctx, req, opts...)
	if err == nil && req.GetExecution() != nil {
		if response.GetState().GetState() != builderv0.DeploymentStatus_SUCCESS {
			return nil, fmt.Errorf("bound render did not succeed: %s", response.GetState().GetMessage())
		}
		if err := artifactexecution.CheckReceipt(req.GetExecution(), response.GetExecution()); err != nil {
			return nil, err
		}
	}
	return response, err
}
