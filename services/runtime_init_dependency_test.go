package services

import (
	"context"
	"net"
	"testing"

	agentservices "github.com/codefly-dev/core/agents/services"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/network"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

type dependencyInitServer struct {
	runtimev0.UnimplementedRuntimeServer
	requests chan *runtimev0.InitRequest
}

func (s *dependencyInitServer) Init(_ context.Context, req *runtimev0.InitRequest) (*runtimev0.InitResponse, error) {
	s.requests <- req
	return &runtimev0.InitResponse{Status: &runtimev0.InitStatus{State: runtimev0.InitStatus_READY}}, nil
}

func TestRuntimeInitDependencyMappingsCrossWireWithVisibilityAndSelection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	peer := &dependencyInitServer{requests: make(chan *runtimev0.InitRequest, 1)}
	runtimev0.RegisterRuntimeServer(server, peer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	instance := &RuntimeInstance{
		Instance: &Instance{Module: &resources.Module{Name: "consumer"}, Service: &resources.Service{
			ServiceDependencies: []*resources.ServiceDependency{{Module: "producer", Name: "api", Endpoints: []*resources.EndpointReference{{Name: "read"}}}},
		}},
		Runtime: &agentservices.RuntimeAgent{RuntimeClient: runtimev0.NewRuntimeClient(conn)},
	}
	endpoint := &basev0.Endpoint{Module: "producer", Service: "api", Name: "read", Api: "grpc", Visibility: resources.VisibilityInternal, AllowModules: []string{"consumer"}}
	mapping := &basev0.NetworkMapping{Endpoint: endpoint, Instances: []*basev0.NetworkInstance{network.Native(endpoint, uint16(listener.Addr().(*net.TCPAddr).Port))}}
	request := &runtimev0.InitRequest{DependenciesNetworkMappings: []*basev0.NetworkMapping{
		{Endpoint: &basev0.Endpoint{Module: "producer", Service: "api", Name: "admin", Api: "grpc", Visibility: resources.VisibilityPrivate}},
		mapping,
	}}
	_, err = instance.Init(context.Background(), request)
	require.NoError(t, err)
	received := <-peer.requests
	require.Len(t, received.DependenciesNetworkMappings, 1)
	require.True(t, proto.Equal(mapping, received.DependenciesNetworkMappings[0]))
	require.Len(t, request.DependenciesNetworkMappings, 2, "filtering must not mutate the caller's request")

	endpoint.Visibility = resources.VisibilityPrivate
	_, err = instance.Init(context.Background(), request)
	require.ErrorContains(t, err, "private to module")
	require.Empty(t, peer.requests, "a private dependency must not reach the runtime")

	_, err = instance.Init(context.Background(), &runtimev0.InitRequest{})
	require.NoError(t, err, "the additive field is optional for pre-existing senders")
	require.Empty(t, (<-peer.requests).GetDependenciesNetworkMappings())
}
