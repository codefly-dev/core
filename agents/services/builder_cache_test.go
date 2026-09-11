package services

import (
	"context"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type cacheBuilderClient struct {
	builderv0.BuilderClient
	version string
	calls   int
}

func (c *cacheBuilderClient) Build(context.Context, *builderv0.BuildRequest, ...grpc.CallOption) (*builderv0.BuildResponse, error) {
	c.calls++
	return &builderv0.BuildResponse{State: &builderv0.BuildStatus{State: builderv0.BuildStatus_SUCCESS}, CacheContractVersion: c.version}, nil
}
func TestBuildCacheRequiresAgentAcknowledgement(t *testing.T) {
	client := &cacheBuilderClient{}
	agent := &BuilderAgent{BuilderClient: client}
	req := &builderv0.BuildRequest{}
	_, err := agent.Build(context.Background(), req)
	require.NoError(t, err)
	cache := &builderv0.BuildCacheOptions{Backend: "registry", Scope: "service"}
	req.BuildContext = &builderv0.BuildContext{Kind: &builderv0.BuildContext_DockerBuildContext{DockerBuildContext: &builderv0.DockerBuildContext{Cache: cache}}}
	_, err = agent.Build(context.Background(), req)
	require.ErrorContains(t, err, "upgrade the agent")
	client.version = "registry-v1"
	_, err = agent.Build(context.Background(), req)
	require.NoError(t, err)
	cache.Backend = "unsupported"
	_, err = agent.Build(context.Background(), req)
	require.ErrorContains(t, err, "unsupported")
	require.Equal(t, 3, client.calls)
}
