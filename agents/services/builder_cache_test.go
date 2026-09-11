package services

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
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

type buildxTestServer struct {
	*DefaultBuilder
	capabilities    *builderv0.BuildCapabilitiesResponse
	capabilityError error
	acknowledgement string
	buildCalls      atomic.Int32
}

func (s *buildxTestServer) BuildCapabilities(context.Context, *builderv0.BuildCapabilitiesRequest) (*builderv0.BuildCapabilitiesResponse, error) {
	return s.capabilities, s.capabilityError
}

func (s *buildxTestServer) Build(ctx context.Context, req *builderv0.BuildRequest) (*builderv0.BuildResponse, error) {
	s.buildCalls.Add(1)
	resp, err := s.DefaultBuilder.Build(ctx, req)
	if resp != nil {
		resp.BuildxBuilder = s.acknowledgement
	}
	return resp, err
}

func TestBuildxSelectionRequiresExecutorAcknowledgementOverGRPC(t *testing.T) {
	for _, recipe := range []bool{false, true} {
		t.Run(fmt.Sprintf("recipe=%t", recipe), func(t *testing.T) {
			wrapper := &BuilderWrapper{Base: &Base{loaded: true}}
			if recipe {
				wrapper.BuildResult = &builderv0.BuildResult{Kind: &builderv0.BuildResult_DockerBuildPlan{DockerBuildPlan: &builderv0.DockerBuildPlan{}}}
			}
			server := grpc.NewServer()
			builderv0.RegisterBuilderServer(server, &buildxTestServer{
				DefaultBuilder: NewDefaultBuilder(wrapper),
				capabilities:   &builderv0.BuildCapabilitiesResponse{BuildxSelection: true},
			})
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

func TestBuildxCapabilitiesCheckedBeforeBuildOverGRPC(t *testing.T) {
	// Merely embedding the updated Core server must never advertise support.
	_, err := NewDefaultBuilder(&BuilderWrapper{}).BuildCapabilities(t.Context(), &builderv0.BuildCapabilitiesRequest{})
	require.Equal(t, codes.Unimplemented, status.Code(err))
	for _, tc := range []struct {
		name       string
		legacy     bool
		supported  bool
		probeError error
		ack        string
		wantCalls  int32
		wantError  string
	}{
		{name: "old wire service", legacy: true, wantError: "before execution"},
		{name: "unsupported", wantError: "refusing to execute"},
		{name: "unimplemented default", probeError: status.Error(codes.Unimplemented, "not implemented"), wantError: "before execution"},
		{name: "unavailable", probeError: status.Error(codes.Unavailable, "offline"), wantError: "before execution"},
		{name: "supported", supported: true, ack: "selected", wantCalls: 1},
		{name: "mismatched acknowledgement", supported: true, ack: "other", wantCalls: 1, wantError: "did not acknowledge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			implementation := &buildxTestServer{
				DefaultBuilder:  NewDefaultBuilder(&BuilderWrapper{Base: &Base{loaded: true}}),
				capabilities:    &builderv0.BuildCapabilitiesResponse{BuildxSelection: tc.supported},
				capabilityError: tc.probeError,
				acknowledgement: tc.ack,
			}
			server := grpc.NewServer()
			if tc.legacy {
				// Match an older binary's wire service: the probe RPC does not exist.
				descriptor := builderv0.Builder_ServiceDesc
				descriptor.Methods = nil
				for _, method := range builderv0.Builder_ServiceDesc.Methods {
					if method.MethodName != "BuildCapabilities" {
						descriptor.Methods = append(descriptor.Methods, method)
					}
				}
				server.RegisterService(&descriptor, implementation)
			} else {
				builderv0.RegisterBuilderServer(server, implementation)
			}
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
			request := &builderv0.BuildRequest{BuildContext: &builderv0.BuildContext{Kind: &builderv0.BuildContext_DockerBuildContext{DockerBuildContext: &builderv0.DockerBuildContext{BuildxBuilder: "selected"}}}}
			// A recipe request is not proof that an old agent will avoid execution.
			request.OutputDirectory = t.TempDir()
			_, err = client.Build(ctx, request)
			if tc.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantError)
			}
			require.Equal(t, tc.wantCalls, implementation.buildCalls.Load())
			// No selection retains compatibility and never requires the probe.
			request.GetBuildContext().GetDockerBuildContext().BuildxBuilder = ""
			_, err = client.Build(ctx, request)
			require.NoError(t, err)
			require.Equal(t, tc.wantCalls+1, implementation.buildCalls.Load())
		})
	}
}
