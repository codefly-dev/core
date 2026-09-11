package services

import (
	"context"
	"fmt"
	"google.golang.org/grpc/credentials/insecure"
	"net"
	"testing"
	"time"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type cacheBuilderClient struct {
	builderv0.BuilderClient
	version string
	calls   int
	plan    *builderv0.DockerBuildPlan
}

func (c *cacheBuilderClient) Build(context.Context, *builderv0.BuildRequest, ...grpc.CallOption) (*builderv0.BuildResponse, error) {
	c.calls++
	result := &builderv0.BuildResult{}
	if c.plan != nil {
		result.Kind = &builderv0.BuildResult_DockerBuildPlan{DockerBuildPlan: c.plan}
	}
	return &builderv0.BuildResponse{State: &builderv0.BuildStatus{State: builderv0.BuildStatus_SUCCESS}, CacheContractVersion: c.version, Result: result}, nil
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

func TestRecipeAgentsDoNotAcknowledgeCallerOwnedCacheExecution(t *testing.T) {
	directory := writeRecipeTree(t, true)
	plan, err := SingleImageBuildPlan(directory, "repo/app:v1", RecipeBuildPlatforms())
	require.NoError(t, err)
	client := &cacheBuilderClient{plan: plan}
	agent := &BuilderAgent{BuilderClient: client}
	req := &builderv0.BuildRequest{OutputDirectory: directory, BuildContext: &builderv0.BuildContext{Kind: &builderv0.BuildContext_DockerBuildContext{DockerBuildContext: &builderv0.DockerBuildContext{Cache: &builderv0.BuildCacheOptions{Backend: "registry", Scope: "service", Exports: []string{"ghcr.io/org/cache"}}}}}}
	response, err := agent.Build(context.Background(), req)
	require.NoError(t, err)
	require.Empty(t, response.CacheContractVersion)
	require.NoError(t, VerifyDockerBuildPlan(directory, response.Result.GetDockerBuildPlan()))
	client.plan = nil
	_, err = agent.Build(context.Background(), req)
	require.ErrorContains(t, err, "upgrade the agent")
}

func TestBuildxSelectionRequiresExecutorAcknowledgementOverGRPC(t *testing.T) {
	for _, recipe := range []bool{false, true} {
		t.Run(fmt.Sprintf("recipe=%t", recipe), func(t *testing.T) {
			wrapper := &BuilderWrapper{Base: &Base{loaded: true}}
			if recipe {
				wrapper.BuildResult = &builderv0.BuildResult{Kind: &builderv0.BuildResult_DockerBuildPlan{DockerBuildPlan: &builderv0.DockerBuildPlan{}}}
			}
			server := grpc.NewServer()
			builderv0.RegisterBuilderServer(server, NewDefaultBuilder(wrapper))
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)
			conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			client := NewBuilderAgentClient(conn)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			_, err = client.Build(ctx, &builderv0.BuildRequest{})
			require.NoError(t, err)
			request := &builderv0.BuildRequest{BuildContext: &builderv0.BuildContext{Kind: &builderv0.BuildContext_DockerBuildContext{DockerBuildContext: &builderv0.DockerBuildContext{BuildxBuilder: "selected"}}}}
			_, err = client.Build(ctx, request)
			if recipe {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "did not acknowledge requested Buildx builder")
			}
		})
	}
}
