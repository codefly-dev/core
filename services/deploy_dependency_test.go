package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	agentservices "github.com/codefly-dev/core/agents/services"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type recordingBuilderClient struct {
	builderv0.BuilderClient
	request *builderv0.DeploymentRequest
}

func (client *recordingBuilderClient) Deploy(_ context.Context, request *builderv0.DeploymentRequest, _ ...grpc.CallOption) (*builderv0.DeploymentResponse, error) {
	client.request = request
	return &builderv0.DeploymentResponse{State: &builderv0.DeploymentStatus{State: builderv0.DeploymentStatus_SUCCESS}}, nil
}

func accountsConsumer(endpoints ...string) *resources.Service {
	dependency := &resources.ServiceDependency{Module: "saas", Name: "accounts", Kind: resources.DependencyKindRuntime}
	for _, endpoint := range endpoints {
		dependency.Endpoints = append(dependency.Endpoints, &resources.EndpointReference{Name: endpoint})
	}
	return &resources.Service{ServiceDependencies: []*resources.ServiceDependency{dependency}}
}

func twoModules() *resources.Workspace {
	return &resources.Workspace{Name: "test", Modules: []*resources.ModuleReference{{Name: "platform"}, {Name: "saas"}}}
}

// A deploy hands the consumer its dependencies' addresses just as a run does,
// so the CLI-side wrapper refuses an endpoint the producer keeps private —
// here, with the composition in hand, exactly as Init and Start do. This is
// the property main held in the builder agent; it lives in the provider now,
// because the agent holds no composition to judge an edge with. The incident
// it guards: a workspace that failed static validation, failed to run, and
// still deployed, shipping the private endpoint's address to the cluster.
func TestBuilderDeployRefusesDependencyOnPrivateEndpoint(t *testing.T) {
	client := &recordingBuilderClient{}
	instance := &BuilderInstance{
		Instance: &Instance{Workspace: twoModules(), Module: &resources.Module{Name: "platform"}, Service: accountsConsumer("usage")},
		Builder:  &agentservices.BuilderAgent{BuilderClient: client},
	}
	request := &builderv0.DeploymentRequest{DependenciesNetworkMappings: []*basev0.NetworkMapping{
		{Endpoint: &basev0.Endpoint{Module: "saas", Service: "accounts", Name: "usage", Api: "grpc", Visibility: resources.VisibilityPrivate}},
	}}
	_, err := instance.Deploy(context.Background(), request)
	require.ErrorContains(t, err, `private to module "saas"`)
	require.Nil(t, client.request, "the builder must not deploy when the edge is refused")
	require.Len(t, request.GetDependenciesNetworkMappings(), 1, "filtering must not mutate the caller's request")

	// The permitted sibling is handed over, and only what the dependency
	// names.
	request = &builderv0.DeploymentRequest{DependenciesNetworkMappings: []*basev0.NetworkMapping{
		{Endpoint: &basev0.Endpoint{Module: "saas", Service: "accounts", Name: "grpc", Api: "grpc", Visibility: resources.VisibilityPrivate}},
		{Endpoint: &basev0.Endpoint{Module: "saas", Service: "accounts", Name: "usage", Api: "grpc", Visibility: resources.VisibilityInternal}},
	}}
	_, err = instance.Deploy(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, client.request.GetDependenciesNetworkMappings(), 1)
	require.Equal(t, "usage", client.request.GetDependenciesNetworkMappings()[0].GetEndpoint().GetName())
}

// composedProduct writes a product that composes a platform workspace (module
// saas) and declares one solution (wiki), and loads it: the one shape in which
// a member is a solution, which no in-memory workspace can state.
func composedProduct(t *testing.T) *resources.Workspace {
	t.Helper()
	root := t.TempDir()
	write := func(relative, content string) {
		file := filepath.Join(root, relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
		require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
	}
	write("core/workspace.codefly.yaml", "name: platform-core\nlayout: modules\nmodules:\n  - name: saas\n")
	write("core/modules/saas/module.codefly.yaml", "kind: module\nname: saas\nservices: []\n")
	write("product/workspace.codefly.yaml", "name: product\nlayout: modules\nworkspaces:\n  - name: platform-core\n    path: ../core\nsolutions:\n  - name: wiki\n    path: solutions/wiki\n")
	write("product/solutions/wiki/module.codefly.yaml", "kind: module\nname: wiki\nservices: []\n")
	workspace, err := resources.LoadWorkspaceFromDir(context.Background(), filepath.Join(root, "product"))
	require.NoError(t, err)
	return workspace
}

// The run and deploy hand-outs judge with the composition the instance was
// built for: a solution's route to a module's endpoint is refused on both,
// whatever the endpoint's visibility grants, and an instance that carries no
// composition is refused as unjudged rather than read as a module.
func TestInstanceHandoutsJudgeWithTheComposition(t *testing.T) {
	ctx := context.Background()
	product := composedProduct(t)
	mappings := []*basev0.NetworkMapping{
		{Endpoint: &basev0.Endpoint{Module: "saas", Service: "accounts", Name: "usage", Api: "grpc", Visibility: resources.VisibilityInternal}},
	}
	wiki := &Instance{Workspace: product, Module: &resources.Module{Name: "wiki"}, Service: accountsConsumer("usage")}

	runtime := &recordingRuntimeClient{}
	_, err := (&RuntimeInstance{Instance: wiki, Runtime: &agentservices.RuntimeAgent{RuntimeClient: runtime}}).Start(ctx, &runtimev0.StartRequest{DependenciesNetworkMappings: mappings})
	require.ErrorIs(t, err, resources.ErrSolutionReachesThroughHost, "the run hand-out refuses a solution's route")
	require.Nil(t, runtime.request)
	_, err = (&RuntimeInstance{Instance: wiki, Runtime: &agentservices.RuntimeAgent{RuntimeClient: runtime}}).Init(ctx, &runtimev0.InitRequest{DependenciesNetworkMappings: mappings})
	require.ErrorIs(t, err, resources.ErrSolutionReachesThroughHost)

	builder := &recordingBuilderClient{}
	_, err = (&BuilderInstance{Instance: wiki, Builder: &agentservices.BuilderAgent{BuilderClient: builder}}).Deploy(ctx, &builderv0.DeploymentRequest{DependenciesNetworkMappings: mappings})
	require.ErrorIs(t, err, resources.ErrSolutionReachesThroughHost, "the deploy hand-out refuses a solution's route")
	require.Nil(t, builder.request)

	// The same edge from a module of the host is handed over.
	saas := &Instance{Workspace: product, Module: &resources.Module{Name: "saas"}, Service: accountsConsumer("usage")}
	_, err = (&BuilderInstance{Instance: saas, Builder: &agentservices.BuilderAgent{BuilderClient: builder}}).Deploy(ctx, &builderv0.DeploymentRequest{DependenciesNetworkMappings: mappings})
	require.NoError(t, err)
	require.Len(t, builder.request.GetDependenciesNetworkMappings(), 1)

	// No composition at all: refused, never answered as if the module were a
	// module of something.
	unjudged := &Instance{Module: &resources.Module{Name: "wiki"}, Service: accountsConsumer("usage")}
	_, err = (&RuntimeInstance{Instance: unjudged, Runtime: &agentservices.RuntimeAgent{RuntimeClient: runtime}}).Start(ctx, &runtimev0.StartRequest{DependenciesNetworkMappings: mappings})
	require.ErrorIs(t, err, resources.ErrUnjudgedProvenance)
	_, err = (&BuilderInstance{Instance: unjudged, Builder: &agentservices.BuilderAgent{BuilderClient: builder}}).Deploy(ctx, &builderv0.DeploymentRequest{DependenciesNetworkMappings: mappings})
	require.ErrorIs(t, err, resources.ErrUnjudgedProvenance)
}
