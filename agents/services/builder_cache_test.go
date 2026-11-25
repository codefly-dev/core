package services

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	builderv1 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type cacheBuilderClient struct {
	builderv1.BuilderClient
	version string
	calls   int
	plan    *builderv1.DockerBuildPlan
}

func (c *cacheBuilderClient) Build(context.Context, *builderv1.BuildRequest, ...grpc.CallOption) (*builderv1.BuildResponse, error) {
	c.calls++
	result := &builderv1.BuildResult{}
	if c.plan != nil {
		result.Kind = &builderv1.BuildResult_DockerBuildPlan{DockerBuildPlan: c.plan}
	}
	return &builderv1.BuildResponse{State: &builderv1.BuildStatus{State: builderv1.BuildStatus_SUCCESS}, CacheContractVersion: c.version, Result: result}, nil
}
func TestBuildCacheRequiresAgentAcknowledgement(t *testing.T) {
	client := &cacheBuilderClient{}
	agent := &BuilderAgent{BuilderClient: client}
	req := &builderv1.BuildRequest{}
	_, err := agent.Build(context.Background(), req)
	require.NoError(t, err)
	cache := &builderv1.BuildCacheOptions{Backend: "registry", Scope: "service"}
	req.BuildContext = &builderv1.BuildContext{Kind: &builderv1.BuildContext_DockerBuildContext{DockerBuildContext: &builderv1.DockerBuildContext{Cache: cache}}}
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
	directory, emitted := writeRecipeTree(t, true)
	plan, err := SingleImageBuildPlan(directory, "repo/app:v1", RecipeBuildPlatforms(), emitted)
	require.NoError(t, err)
	client := &cacheBuilderClient{plan: plan}
	agent := &BuilderAgent{BuilderClient: client}
	req := &builderv1.BuildRequest{OutputDirectory: directory, BuildContext: &builderv1.BuildContext{Kind: &builderv1.BuildContext_DockerBuildContext{DockerBuildContext: &builderv1.DockerBuildContext{Cache: &builderv1.BuildCacheOptions{Backend: "registry", Scope: "service", Exports: []string{"ghcr.io/org/cache"}}}}}}
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
	capabilities    *builderv1.BuildCapabilitiesResponse
	capabilityError error
	acknowledgement string
	buildCalls      atomic.Int32
}

func (s *buildxTestServer) BuildCapabilities(context.Context, *builderv1.BuildCapabilitiesRequest) (*builderv1.BuildCapabilitiesResponse, error) {
	return s.capabilities, s.capabilityError
}

func (s *buildxTestServer) Build(ctx context.Context, req *builderv1.BuildRequest) (*builderv1.BuildResponse, error) {
	s.buildCalls.Add(1)
	resp, err := s.DefaultBuilder.Build(ctx, req)
	if resp != nil {
		resp.BuildxBuilder = s.acknowledgement
	}
	return resp, err
}

// Only an in-agent build has an executor to acknowledge: a recipe is built by
// the caller on the builder it selected, and an answer that built nothing (a
// vendor-image agent) has nothing to acknowledge. Treating "no recipe" as
// "built in-agent" refused every render composing a vendor-image agent.
func TestBuildxSelectionRequiresExecutorAcknowledgementOverGRPC(t *testing.T) {
	for name, result := range map[string]*builderv1.BuildResult{
		"in-agent build": {Kind: &builderv1.BuildResult_DockerBuildResult{DockerBuildResult: &builderv1.DockerBuildResult{}}},
		"recipe":         {Kind: &builderv1.BuildResult_DockerBuildPlan{DockerBuildPlan: &builderv1.DockerBuildPlan{}}},
		"nothing built":  nil,
	} {
		t.Run(name, func(t *testing.T) {
			recipe := name != "in-agent build"
			wrapper := &BuilderWrapper{Base: &Base{loaded: true}}
			if result != nil {
				wrapper.BuildResult = result
			}
			server := grpc.NewServer()
			builderv1.RegisterBuilderServer(server, &buildxTestServer{
				DefaultBuilder: NewDefaultBuilder(wrapper),
				capabilities:   &builderv1.BuildCapabilitiesResponse{BuildxSelection: true},
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
			_, err = client.Build(ctx, &builderv1.BuildRequest{})
			require.NoError(t, err)
			request := &builderv1.BuildRequest{BuildContext: &builderv1.BuildContext{Kind: &builderv1.BuildContext_DockerBuildContext{DockerBuildContext: &builderv1.DockerBuildContext{BuildxBuilder: "selected"}}}}
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
	_, err := NewDefaultBuilder(&BuilderWrapper{}).BuildCapabilities(t.Context(), &builderv1.BuildCapabilitiesRequest{})
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
				DefaultBuilder:  NewDefaultBuilder(&BuilderWrapper{Base: &Base{loaded: true}, BuildResult: &builderv1.BuildResult{Kind: &builderv1.BuildResult_DockerBuildResult{DockerBuildResult: &builderv1.DockerBuildResult{}}}}), // an in-agent build: the one answer that owes an acknowledgement
				capabilities:    &builderv1.BuildCapabilitiesResponse{BuildxSelection: tc.supported},
				capabilityError: tc.probeError,
				acknowledgement: tc.ack,
			}
			server := grpc.NewServer()
			if tc.legacy {
				// Match an older binary's wire service: the probe RPC does not exist.
				descriptor := builderv1.Builder_ServiceDesc
				descriptor.Methods = nil
				for _, method := range builderv1.Builder_ServiceDesc.Methods {
					if method.MethodName != "BuildCapabilities" {
						descriptor.Methods = append(descriptor.Methods, method)
					}
				}
				server.RegisterService(&descriptor, implementation)
			} else {
				builderv1.RegisterBuilderServer(server, implementation)
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
			request := &builderv1.BuildRequest{BuildContext: &builderv1.BuildContext{Kind: &builderv1.BuildContext_DockerBuildContext{DockerBuildContext: &builderv1.DockerBuildContext{BuildxBuilder: "selected"}}}}
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
