package services

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"
	builderv1 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v1"
	"github.com/codefly-dev/core/resources"
)

// ownMapping is the service's own REST endpoint as the CLI hands it to a
// Kubernetes render: one instance per access, the container one naming the
// in-cluster Service.
func ownMapping(namespace string) *basev1.NetworkMapping {
	endpoint := &basev1.Endpoint{Module: "module", Service: "service", Name: "rest", Api: "rest", Visibility: resources.VisibilityPublic, Exposure: resources.ExposureNone}
	inCluster := resources.NewHTTPNetworkInstance("service."+namespace+".svc.cluster.local", 8080, false)
	inCluster.Access = resources.NewContainerNetworkAccess()
	public := resources.NewHTTPNetworkInstance("service.example.com", 443, true)
	public.Access = resources.NewPublicNetworkAccess()
	return &basev1.NetworkMapping{Endpoint: endpoint, Instances: []*basev1.NetworkInstance{public, inCluster}}
}

func renderOwnEndpoints(t *testing.T, profile builderv1.KubernetesOutputProfile, inputs DeploymentInputs) string {
	t.Helper()
	ctx := context.Background()
	templates, err := fs.Sub(deploymentTestFS, "testdata/deployment")
	require.NoError(t, err)
	builder, manager := restrictedDeployBuilder(ctx, t)
	destination := t.TempDir()
	response, err := builder.DeployKustomize(ctx, &builderv1.DeploymentRequest{
		Environment:     &basev1.Environment{Name: "test"},
		NetworkMappings: []*basev1.NetworkMapping{ownMapping("platform-example")},
		Deployment: &builderv1.Deployment{Kind: &builderv1.Deployment_Kubernetes{
			Kubernetes: &builderv1.KubernetesDeployment{
				Namespace:   "platform-example",
				Destination: destination,
				Profile:     profile,
			},
		}},
	}, KustomizeDeployment{
		EnvironmentVariables: manager,
		Templates:            templates,
		Inputs:               inputs,
		Parameters:           struct{ Name string }{Name: "self"},
	})
	require.NoError(t, err)
	require.Equal(t, builderv1.DeploymentStatus_SUCCESS, response.GetState().GetState(), response.GetState().GetMessage())
	configMap, err := os.ReadFile(filepath.Join(destination, "base", "config-map.yaml"))
	require.NoError(t, err)
	return string(configMap)
}

// A rendered service listens on localhost but must be able to tell a peer —
// a gateway it registers with — how to call it back. Both carriers are
// rendered, under different names, for every output profile.
func TestDeployKustomizeRendersSelfEndpointBesideLocalizedListenEndpoint(t *testing.T) {
	for _, profile := range []builderv1.KubernetesOutputProfile{
		builderv1.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1,
		builderv1.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
	} {
		t.Run(profile.String(), func(t *testing.T) {
			manifest := renderOwnEndpoints(t, profile, DeploymentInputs{OwnEndpoints: true})
			// The listen address is unchanged: localized, and (as before this
			// carrier existed) a bare host:port.
			require.Contains(t, manifest, `CODEFLY__ENDPOINT__MODULE__SERVICE__REST__REST: "localhost:8080"`)
			// The advertised address is the in-cluster Service, never localhost
			// and never the public instance.
			require.Contains(t, manifest, `CODEFLY__SELF_ENDPOINT__MODULE__SERVICE__REST__REST: "http://service.platform-example.svc.cluster.local:8080"`)
			require.NotContains(t, manifest, "service.example.com")
			// The workload says it is deployed.
			require.Contains(t, manifest, `CODEFLY__RUNTIME_CONTEXT: "kubernetes"`)
		})
	}
}

func TestDeployKustomizeOmitsSelfEndpointWhenOwnEndpointsAreNotAnInput(t *testing.T) {
	manifest := renderOwnEndpoints(t, builderv1.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1, DeploymentInputs{})
	require.NotContains(t, manifest, resources.SelfEndpointPrefix)
	require.NotContains(t, manifest, resources.EndpointPrefix+"__MODULE__SERVICE")
	require.Contains(t, manifest, `CODEFLY__RUNTIME_CONTEXT: "kubernetes"`)
}

func runMapping() *basev1.NetworkMapping {
	endpoint := &basev1.Endpoint{Module: "module", Service: "service", Name: "rest", Api: "rest", Visibility: resources.VisibilityPublic, Exposure: resources.ExposureNone}
	native := resources.NewHTTPNetworkInstance("localhost", 33123, false)
	native.Access = resources.NewNativeNetworkAccess()
	container := resources.NewHTTPNetworkInstance("host.docker.internal", 33123, false)
	container.Access = resources.NewContainerNetworkAccess()
	return &basev1.NetworkMapping{Endpoint: endpoint, Instances: []*basev1.NetworkInstance{native, container}}
}

// In a local run a peer shares the service's runtime access, so the advertised
// address is the instance under NetworkAccess(): host-native for a native or
// nix process, the container network for a container process.
func TestRuntimeAddSelfEndpointsFollowsTheRuntimeAccess(t *testing.T) {
	for _, tc := range []struct {
		runtimeContext *basev1.RuntimeContext
		want           string
	}{
		{resources.NewRuntimeContextNative(), "http://localhost:33123"},
		{resources.NewRuntimeContextNix(), "http://localhost:33123"},
		{resources.NewRuntimeContextContainer(), "http://host.docker.internal:33123"},
	} {
		t.Run(tc.runtimeContext.GetKind(), func(t *testing.T) {
			wrapper := fixtureRuntimeWrapper()
			wrapper.WithContext(tc.runtimeContext)
			wrapper.NetworkMappings = []*basev1.NetworkMapping{runMapping()}
			require.NoError(t, wrapper.AddSelfEndpoints(context.Background()))

			environment := serviceEnvironment(t, wrapper)
			require.Contains(t, environment, "CODEFLY__SELF_ENDPOINT__MODULE__SERVICE__REST__REST="+tc.want)

			instance, err := resources.FindSelfNetworkInstanceInEnvironmentVariables(context.Background(),
				&resources.EndpointInformation{Module: "module", Service: "service", Name: "rest", API: "rest"}, environment)
			require.NoError(t, err)
			require.Equal(t, tc.want, instance.Address)
		})
	}
}

func TestRuntimeAddSelfEndpointsRefusesAnUninitializedRuntime(t *testing.T) {
	require.Error(t, (&RuntimeWrapper{}).AddSelfEndpoints(context.Background()))
}
