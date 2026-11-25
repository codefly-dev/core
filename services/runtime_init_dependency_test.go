package services

import (
	"context"
	"net"
	"testing"

	agentservices "github.com/codefly-dev/core/agents/services"
	basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"
	runtimev1 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v1"
	"github.com/codefly-dev/core/network"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

type dependencyInitServer struct {
	runtimev1.UnimplementedRuntimeServer
	requests chan *runtimev1.InitRequest
}

func (s *dependencyInitServer) Init(_ context.Context, req *runtimev1.InitRequest) (*runtimev1.InitResponse, error) {
	s.requests <- req
	return &runtimev1.InitResponse{Status: &runtimev1.InitStatus{State: runtimev1.InitStatus_READY}}, nil
}

func TestRuntimeInitDependencyMappingsCrossWireWithVisibilityAndSelection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	peer := &dependencyInitServer{requests: make(chan *runtimev1.InitRequest, 1)}
	runtimev1.RegisterRuntimeServer(server, peer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	instance := &RuntimeInstance{
		Instance: &Instance{Workspace: &resources.Workspace{Name: "test", Modules: []*resources.ModuleReference{{Name: "consumer"}, {Name: "producer"}}}, Module: &resources.Module{Name: "consumer"}, Service: &resources.Service{
			ServiceDependencies: []*resources.ServiceDependency{{Module: "producer", Name: "api", Endpoints: []*resources.EndpointReference{{Name: "read"}}}},
		}},
		Runtime: &agentservices.RuntimeAgent{RuntimeClient: runtimev1.NewRuntimeClient(conn)},
	}
	endpoint := &basev1.Endpoint{Module: "producer", Service: "api", Name: "read", Api: "grpc", Visibility: resources.VisibilityInternal}
	mapping := &basev1.NetworkMapping{Endpoint: endpoint, Instances: []*basev1.NetworkInstance{network.Native(endpoint, uint16(listener.Addr().(*net.TCPAddr).Port))}}
	request := &runtimev1.InitRequest{DependenciesNetworkMappings: []*basev1.NetworkMapping{
		{Endpoint: &basev1.Endpoint{Module: "producer", Service: "api", Name: "admin", Api: "grpc", Visibility: resources.VisibilityPrivate}},
		mapping,
	}}
	_, err = instance.Init(context.Background(), request)
	require.NoError(t, err)
	received := <-peer.requests
	require.Len(t, received.DependenciesNetworkMappings, 1)
	require.True(t, proto.Equal(mapping, received.DependenciesNetworkMappings[0]))
	require.Len(t, request.DependenciesNetworkMappings, 2, "filtering must not mutate the caller's request")

	// Private: the one visibility that denies a consumer in another module.
	endpoint.Visibility = resources.VisibilityPrivate
	_, err = instance.Init(context.Background(), request)
	require.ErrorContains(t, err, "private to module")
	require.Empty(t, peer.requests, "a private dependency must not reach the runtime")

	_, err = instance.Init(context.Background(), &runtimev1.InitRequest{})
	require.NoError(t, err, "the additive field is optional for pre-existing senders")
	require.Empty(t, (<-peer.requests).GetDependenciesNetworkMappings())
}
