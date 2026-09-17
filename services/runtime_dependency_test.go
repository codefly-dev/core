package services

import (
	"context"
	"testing"

	agentservices "github.com/codefly-dev/core/agents/services"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type recordingRuntimeClient struct {
	runtimev0.RuntimeClient
	request *runtimev0.StartRequest
}

func (client *recordingRuntimeClient) Start(_ context.Context, request *runtimev0.StartRequest, _ ...grpc.CallOption) (*runtimev0.StartResponse, error) {
	client.request = request
	return &runtimev0.StartResponse{Status: &runtimev0.StartStatus{State: runtimev0.StartStatus_STARTED}}, nil
}

func TestRuntimeStartForwardsOnlyDeclaredDependencyMappings(t *testing.T) {
	client := &recordingRuntimeClient{}
	service := &resources.Service{ServiceDependencies: []*resources.ServiceDependency{{
		Module:    "saas",
		Name:      "accounts",
		Endpoints: []*resources.EndpointReference{{Name: "usage"}},
	}}}
	instance := &RuntimeInstance{
		Instance: &Instance{Module: &resources.Module{Name: "platform"}, Service: service},
		Runtime:  &agentservices.RuntimeAgent{RuntimeClient: client},
	}
	// The unconsumed sibling is private on purpose: narrowing happens before
	// visibility is judged, so an endpoint this service never named must not
	// decide whether it may start.
	request := &runtimev0.StartRequest{DependenciesNetworkMappings: []*basev0.NetworkMapping{
		{Endpoint: &basev0.Endpoint{Module: "saas", Service: "accounts", Name: "grpc", Api: "grpc", Visibility: resources.VisibilityPrivate}},
		{Endpoint: &basev0.Endpoint{Module: "saas", Service: "accounts", Name: "usage", Api: "grpc", Visibility: resources.VisibilityPublic}},
	}}

	_, err := instance.Start(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, client.request.GetDependenciesNetworkMappings(), 1)
	require.Equal(t, "usage", client.request.GetDependenciesNetworkMappings()[0].GetEndpoint().GetName())
	require.Len(t, request.GetDependenciesNetworkMappings(), 2)
}

// A run must refuse a cross-module dependency on an endpoint whose producer
// keeps it private, because the static workspace pass refuses the same edge.
// Nothing on this path used to ask, so a workspace that failed validation still
// started, registered, and served — the two disagreed silently.
func TestRuntimeStartRefusesDependencyOnPrivateEndpoint(t *testing.T) {
	client := &recordingRuntimeClient{}
	service := &resources.Service{ServiceDependencies: []*resources.ServiceDependency{{
		Module:    "saas",
		Name:      "accounts",
		Endpoints: []*resources.EndpointReference{{Name: "grpc"}},
	}}}
	instance := &RuntimeInstance{
		Instance: &Instance{Module: &resources.Module{Name: "platform"}, Service: service},
		Runtime:  &agentservices.RuntimeAgent{RuntimeClient: client},
	}
	request := &runtimev0.StartRequest{DependenciesNetworkMappings: []*basev0.NetworkMapping{
		{Endpoint: &basev0.Endpoint{Module: "saas", Service: "accounts", Name: "grpc", Api: "grpc", Visibility: resources.VisibilityPrivate}},
	}}

	_, err := instance.Start(context.Background(), request)
	require.ErrorContains(t, err, "private to module \"saas\"")
	require.Nil(t, client.request, "the agent must not be started when the edge is refused")
}

// An endpoint a producer declares reachable from this module must still start.
func TestRuntimeStartAllowsDependencyOnAllowListedEndpoint(t *testing.T) {
	client := &recordingRuntimeClient{}
	service := &resources.Service{ServiceDependencies: []*resources.ServiceDependency{{
		Module:    "saas",
		Name:      "accounts",
		Endpoints: []*resources.EndpointReference{{Name: "grpc"}},
	}}}
	instance := &RuntimeInstance{
		Instance: &Instance{Module: &resources.Module{Name: "platform"}, Service: service},
		Runtime:  &agentservices.RuntimeAgent{RuntimeClient: client},
	}
	request := &runtimev0.StartRequest{DependenciesNetworkMappings: []*basev0.NetworkMapping{
		{Endpoint: &basev0.Endpoint{
			Module: "saas", Service: "accounts", Name: "grpc", Api: "grpc",
			Visibility: resources.VisibilityInternal, AllowModules: []string{"platform"},
		}},
	}}

	_, err := instance.Start(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, client.request)
}
