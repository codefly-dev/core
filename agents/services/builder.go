package services

import (
	"context"
	"fmt"

	dockerhelpers "github.com/codefly-dev/core/agents/helpers/docker"
	"github.com/codefly-dev/core/agents/manager"
	"github.com/codefly-dev/core/resources"

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

// Build requires cache acknowledgement only when the agent executes the image
// build. Recipe execution, including cache policy, belongs to the caller.
func (b *BuilderAgent) Build(ctx context.Context, req *builderv0.BuildRequest, opts ...grpc.CallOption) (*builderv0.BuildResponse, error) {
	cache := req.GetBuildContext().GetDockerBuildContext().GetCache()
	if cache != nil {
		if _, err := dockerhelpers.CacheArguments(cache, RecipeBuildPlatforms()); err != nil {
			return nil, err
		}
	}
	resp, err := b.BuilderClient.Build(ctx, req, opts...)
	if err == nil && cache != nil && resp.GetState().GetState() == builderv0.BuildStatus_SUCCESS && resp.GetResult().GetDockerBuildPlan() == nil && resp.GetCacheContractVersion() != dockerhelpers.CacheContractVersion {
		return nil, fmt.Errorf("builder agent did not acknowledge build cache contract %s; upgrade the agent or remove cache options", dockerhelpers.CacheContractVersion)
	}
	if selected := req.GetBuildContext().GetDockerBuildContext().GetBuildxBuilder(); selected != "" && err == nil && resp.GetState().GetState() == builderv0.BuildStatus_SUCCESS && resp.GetResult().GetDockerBuildPlan() == nil && resp.GetBuildxBuilder() != selected {
		return nil, fmt.Errorf("builder agent did not acknowledge requested Buildx builder %q; upgrade the agent", selected)
	}
	return resp, err
}
