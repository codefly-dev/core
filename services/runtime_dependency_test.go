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
		Instance: &Instance{Service: service},
		Runtime:  &agentservices.RuntimeAgent{RuntimeClient: client},
	}
	request := &runtimev0.StartRequest{DependenciesNetworkMappings: []*basev0.NetworkMapping{
		{Endpoint: &basev0.Endpoint{Module: "saas", Service: "accounts", Name: "grpc", Api: "grpc"}},
		{Endpoint: &basev0.Endpoint{Module: "saas", Service: "accounts", Name: "usage", Api: "grpc"}},
	}}

	_, err := instance.Start(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, client.request.GetDependenciesNetworkMappings(), 1)
	require.Equal(t, "usage", client.request.GetDependenciesNetworkMappings()[0].GetEndpoint().GetName())
	require.Len(t, request.GetDependenciesNetworkMappings(), 2)
}
