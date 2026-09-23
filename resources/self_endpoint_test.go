package resources_test

import (
	"context"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func selfMapping() *basev0.NetworkMapping {
	endpoint := &basev0.Endpoint{Module: "platform-example", Service: "back-end", Name: "rest", Api: "rest"}
	native := resources.NewHTTPNetworkInstance("localhost", 31000, false)
	native.Access = resources.NewNativeNetworkAccess()
	inCluster := resources.NewHTTPNetworkInstance("back-end.platform-example.svc.cluster.local", 8080, false)
	inCluster.Access = resources.NewContainerNetworkAccess()
	return &basev0.NetworkMapping{Endpoint: endpoint, Instances: []*basev0.NetworkInstance{native, inCluster}}
}

func TestSelfEndpointKeyUsesTheEndpointNormalization(t *testing.T) {
	info := &resources.EndpointInformation{Module: "platform-example", Service: "back-end", Name: "rest", API: "rest"}
	require.Equal(t, "CODEFLY__SELF_ENDPOINT__PLATFORM_EXAMPLE__BACK_END__REST__REST", resources.SelfEndpointAsEnvironmentVariableKey(info))
	require.Equal(t, "CODEFLY__ENDPOINT__PLATFORM_EXAMPLE__BACK_END__REST__REST", resources.EndpointAsEnvironmentVariableKey(info))
	// A reader that collects CODEFLY__ENDPOINT__ carriers must never pick the
	// advertised address up as an endpoint.
	require.False(t, strings.HasPrefix(resources.SelfEndpointAsEnvironmentVariableKey(info), resources.EndpointPrefix+"__"))
}

func TestAddSelfEndpointsKeepsListenAndAdvertisedAddressesApart(t *testing.T) {
	ctx := context.Background()
	manager := resources.NewEnvironmentVariableManager()
	mappings := []*basev0.NetworkMapping{selfMapping()}
	require.NoError(t, manager.AddEndpoints(ctx, resources.LocalizeNetworkMapping(mappings, "localhost"), resources.NewContainerNetworkAccess()))
	require.NoError(t, manager.AddSelfEndpoints(ctx, mappings, resources.NewContainerNetworkAccess()))

	variables, err := manager.All()
	require.NoError(t, err)
	environment := resources.EnvironmentVariableAsStrings(variables)
	require.Contains(t, environment, "CODEFLY__ENDPOINT__PLATFORM_EXAMPLE__BACK_END__REST__REST=localhost:8080")
	require.Contains(t, environment, "CODEFLY__SELF_ENDPOINT__PLATFORM_EXAMPLE__BACK_END__REST__REST=http://back-end.platform-example.svc.cluster.local:8080")
	require.Len(t, manager.SelfEndpoints(), 1)

	info := &resources.EndpointInformation{Module: "platform-example", Service: "back-end", Name: "rest", API: "rest"}
	listen, err := resources.FindNetworkInstanceInEnvironmentVariables(ctx, info, environment)
	require.NoError(t, err)
	require.Equal(t, "localhost", listen.Hostname)
	self, err := resources.FindSelfNetworkInstanceInEnvironmentVariables(ctx, info, environment)
	require.NoError(t, err)
	require.Equal(t, "back-end.platform-example.svc.cluster.local", self.Hostname)
	require.Equal(t, uint16(8080), self.Port)
	require.Equal(t, "http://back-end.platform-example.svc.cluster.local:8080", self.Address)
}

// A missing advertised address is an error, never a silent fallback to the
// listen address: advertising localhost is the defect the carrier prevents.
func TestFindSelfNetworkInstanceDoesNotFallBackToTheListenAddress(t *testing.T) {
	info := &resources.EndpointInformation{Module: "platform-example", Service: "back-end", Name: "rest", API: "rest"}
	_, err := resources.FindSelfNetworkInstanceInEnvironmentVariables(context.Background(), info,
		[]string{"CODEFLY__ENDPOINT__PLATFORM_EXAMPLE__BACK_END__REST__REST=localhost:8080"})
	require.Error(t, err)
}

func TestAddSelfEndpointsSkipsMappingsWithoutTheRequestedAccess(t *testing.T) {
	ctx := context.Background()
	manager := resources.NewEnvironmentVariableManager()
	require.NoError(t, manager.AddSelfEndpoints(ctx, []*basev0.NetworkMapping{nil, {}, selfMapping()}, resources.NewPublicNetworkAccess()))
	require.Empty(t, manager.SelfEndpoints())
}

func TestKubernetesRuntimeContextIsADeployedContainerContext(t *testing.T) {
	kubernetes := resources.NewRuntimeContextKubernetes()
	require.True(t, resources.IsKubernetesRuntimeContext(kubernetes))
	require.False(t, resources.IsKubernetesRuntimeContext(resources.NewRuntimeContextContainer()))
	require.False(t, resources.IsKubernetesRuntimeContext(nil))

	// In-cluster peers are reached through container-access instances.
	require.Equal(t, resources.NetworkAccessContainer, resources.NetworkAccessFromRuntimeContext(kubernetes).GetKind())

	// It is not a local run context: nothing can select it for `codefly run`.
	require.NotContains(t, resources.RuntimeContexts(), resources.RuntimeContextKubernetes)
	require.Error(t, resources.ValidateRuntimeContext(resources.RuntimeContextKubernetes))

	t.Setenv(resources.RuntimeContextPrefix, resources.RuntimeContextKubernetes)
	require.Equal(t, resources.RuntimeContextKubernetes, resources.RuntimeContextFromEnv().GetKind())

	// A deployed workload consumes the container configuration its producers
	// publish, exactly as a container run does.
	container := &basev0.Configuration{Origin: "configuration/app/store", RuntimeContext: resources.NewRuntimeContextContainer()}
	native := &basev0.Configuration{Origin: "configuration/app/store", RuntimeContext: resources.NewRuntimeContextNative()}
	require.Equal(t, []*basev0.Configuration{container}, resources.FilterConfigurations([]*basev0.Configuration{native, container}, kubernetes))
}
