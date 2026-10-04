package configurations_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// A manager view that names its consumer refuses a reference to an endpoint
// that consumer may not reach, instead of resolving it to a permitted sibling
// sharing its API.
//
// This is the end of the path the CLI had to model: the composition root reads
// each consumer's configurations through its own view, and the view is now
// where the consumer's identity lives, so the resolution judges the same
// boundary CheckEndpointReferences judges at plan time.
func TestAManagerViewResolvesForTheConsumerItNames(t *testing.T) {
	ctx := context.Background()
	declared := func(unique string) []*resources.Endpoint {
		if unique != "platform/authority" {
			return nil
		}
		return []*resources.Endpoint{
			{Module: "platform", Service: "authority", Name: "grpc", API: "grpc", Visibility: "private"},
			{Module: "platform", Service: "authority", Name: "admin", API: "grpc", Visibility: "public"},
		}
	}
	mappings := []*basev0.NetworkMapping{
		{
			Endpoint: &basev0.Endpoint{Module: "platform", Service: "authority", Name: "grpc", Api: "grpc"},
			Instances: []*basev0.NetworkInstance{
				{Address: "http://localhost:1111", Access: &basev0.NetworkAccess{Kind: resources.NetworkAccessNative}},
			},
		},
		{
			Endpoint: &basev0.Endpoint{Module: "platform", Service: "authority", Name: "admin", Api: "grpc"},
			Instances: []*basev0.NetworkInstance{
				{Address: "http://localhost:2222", Access: &basev0.NetworkAccess{Kind: resources.NetworkAccessNative}},
			},
		},
	}
	conf := &basev0.Configuration{
		Origin: resources.ConfigurationWorkspace,
		Infos: []*basev0.ConfigurationInformation{{
			Name: "work-context",
			ConfigurationValues: []*basev0.ConfigurationValue{
				{Key: "authority-address", Value: "${endpoint:platform/authority/grpc}"},
			},
		}},
	}

	// A consumer in another module may not reach the private `grpc`.
	outside := resources.EndpointSelectionContext{ConsumerModule: "payments", Declared: declared}
	_, err := resources.InterpolateConfigurationEndpoints(ctx, conf, mappings, resources.NewNativeNetworkAccess(),
		resources.WithConsumer(outside.ConsumerModule, outside.Declared))
	require.Error(t, err)
	require.Contains(t, err.Error(), "private to module")
	require.NotContains(t, err.Error(), "localhost:2222", "the public sibling must not stand in")

	// The producer's own module does, and gets its address.
	resolved, err := resources.InterpolateConfigurationEndpoints(ctx, conf, mappings, resources.NewNativeNetworkAccess(),
		resources.WithConsumer("platform", declared))
	require.NoError(t, err)
	require.Equal(t, "http://localhost:1111", resolved.GetInfos()[0].GetConfigurationValues()[0].GetValue())

	// And the plan-time check reaches the same two verdicts over the same
	// manifest, which is the property that makes one of them not a surprise
	// after the other passed.
	producer := func(unique string) (*resources.Service, bool) {
		if unique != "platform/authority" {
			return nil, false
		}
		return referenceCheckService("platform", "authority", nil, declared(unique)...), true
	}
	consumer := referenceCheckService("payments", "worker", []string{"work-context"})
	err = configurations.CheckEndpointReferences(conf.GetInfos(), []*resources.Service{consumer}, resources.RunProfile{}, producer)
	require.Error(t, err, "the check refuses what the resolution refuses")
	require.Contains(t, err.Error(), "private to module")

	own := referenceCheckService("platform", "gateway", []string{"work-context"})
	require.NoError(t, configurations.CheckEndpointReferences(conf.GetInfos(), []*resources.Service{own}, resources.RunProfile{}, producer),
		"and permits what it resolves")
}
