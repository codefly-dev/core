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

// Build rejects success from agents that silently ignored requested cache policy.
func (b *BuilderAgent) Build(ctx context.Context, req *builderv0.BuildRequest, opts ...grpc.CallOption) (*builderv0.BuildResponse, error) {
	cache := req.GetBuildContext().GetDockerBuildContext().GetCache()
	if cache != nil {
		if _, err := dockerhelpers.CacheArguments(cache, RecipeBuildPlatforms()); err != nil {
			return nil, err
		}
	}
	resp, err := b.BuilderClient.Build(ctx, req, opts...)
	if err == nil && cache != nil && resp.GetState().GetState() == builderv0.BuildStatus_SUCCESS && resp.GetCacheContractVersion() != dockerhelpers.CacheContractVersion {
		return nil, fmt.Errorf("builder agent did not acknowledge build cache contract %s; upgrade the agent or remove cache options", dockerhelpers.CacheContractVersion)
	}
	return resp, err
}
