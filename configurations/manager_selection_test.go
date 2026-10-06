package configurations_test

import (
	"context"
	"path/filepath"
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
	authority := []*resources.Endpoint{
		{Module: "platform", Service: "authority", Name: "grpc", API: "grpc", Visibility: "private"},
		{Module: "platform", Service: "authority", Name: "admin", API: "grpc", Visibility: "public", Exposure: "none"},
	}
	declared := resources.DeclaredEndpoints(func(unique string) ([]*resources.Endpoint, bool) {
		if unique != "platform/authority" {
			return nil, false
		}
		return authority, true
	})
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
		return referenceCheckService("platform", "authority", nil, authority...), true
	}
	consumer := referenceCheckService("payments", "worker", []string{"work-context"})
	err = configurations.CheckEndpointReferences(conf.GetInfos(), []*resources.Service{consumer}, resources.RunProfile{}, producer)
	require.Error(t, err, "the check refuses what the resolution refuses")
	require.Contains(t, err.Error(), "private to module")

	own := referenceCheckService("platform", "gateway", []string{"work-context"})
	require.NoError(t, configurations.CheckEndpointReferences(conf.GetInfos(), []*resources.Service{own}, resources.RunProfile{}, producer),
		"and permits what it resolves")
}

// The same boundary through an actual manager view: a view that names its
// consumer refuses the private exact name; the producer's own module resolves
// it; a view that names no consumer refuses to interpolate at all rather than
// resolve as though anyone may reach anything.
func TestAManagerViewJudgesTheConsumerItNames(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", "name: solution\nlayout: modules\n")
	writeConfigurationFile(t, root, "solution/configurations/local/work-context.env",
		"authority-address=${endpoint:platform/authority/grpc}\n")
	workspace, err := resources.LoadWorkspaceFromDir(ctx, filepath.Join(root, "solution"))
	require.NoError(t, err)
	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	authority := []*resources.Endpoint{
		{Module: "platform", Service: "authority", Name: "admin", API: "grpc", Visibility: "public", Exposure: "none"},
		{Module: "platform", Service: "authority", Name: "grpc", API: "grpc", Visibility: "private"},
	}
	declared := resources.DeclaredEndpoints(func(unique string) ([]*resources.Endpoint, bool) {
		return authority, unique == "platform/authority"
	})
	mappings := []*basev0.NetworkMapping{
		{Endpoint: &basev0.Endpoint{Module: "platform", Service: "authority", Name: "admin", Api: "grpc"},
			Instances: []*basev0.NetworkInstance{{Address: "http://localhost:2222", Access: resources.NewNativeNetworkAccess()}}},
		{Endpoint: &basev0.Endpoint{Module: "platform", Service: "authority", Name: "grpc", Api: "grpc"},
			Instances: []*basev0.NetworkInstance{{Address: "http://localhost:1111", Access: resources.NewNativeNetworkAccess()}}},
	}
	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader).WithNetworkMappings(mappings, resources.NewNativeNetworkAccess())
	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	_, err = manager.GetWorkspaceDependenciesConfigurations(ctx, "work-context")
	require.ErrorIs(t, err, resources.ErrConsumerNotIdentified, "a view naming no consumer does not resolve")

	_, err = manager.ForConsumerModule("payments", declared).GetWorkspaceDependenciesConfigurations(ctx, "work-context")
	require.Error(t, err)
	require.Contains(t, err.Error(), "private to module")
	require.NotContains(t, err.Error(), "localhost:2222")

	confs, err := manager.ForConsumerModule("platform", declared).GetWorkspaceDependenciesConfigurations(ctx, "work-context")
	require.NoError(t, err)
	address, err := resources.GetConfigurationValue(ctx, confs[0], "work-context", "authority-address")
	require.NoError(t, err)
	require.Equal(t, "http://localhost:1111", address)
}

// The dominance rule through the manager's composition-root read: a root group
// read run-wide by every service, carrying an omittable reference beside an
// ambiguous one, is refused with the group and key — not delivered with the
// key removed.
func TestTheCompositionRootReadRefusesAFaultBehindAnOmission(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeConfigurationFile(t, root, "solution/workspace.codefly.yaml", "name: solution\nlayout: modules\n")
	writeConfigurationFile(t, root, "solution/configurations/local/work-context.env",
		"addresses=${endpoint:platform/authority/hidden},${endpoint:platform/authority/grpc}\n")
	workspace, err := resources.LoadWorkspaceFromDir(ctx, filepath.Join(root, "solution"))
	require.NoError(t, err)
	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	authority := []*resources.Endpoint{
		{Module: "platform", Service: "authority", Name: "hidden", API: "grpc", Visibility: "private"},
		{Module: "platform", Service: "authority", Name: "primary", API: "grpc", Visibility: "public", Exposure: "none"},
		{Module: "platform", Service: "authority", Name: "secondary", API: "grpc", Visibility: "public", Exposure: "none"},
	}
	declared := resources.DeclaredEndpoints(func(unique string) ([]*resources.Endpoint, bool) { return authority, unique == "platform/authority" })
	mapping := func(name, address string) *basev0.NetworkMapping {
		return &basev0.NetworkMapping{Endpoint: &basev0.Endpoint{Module: "platform", Service: "authority", Name: name, Api: "grpc"},
			Instances: []*basev0.NetworkInstance{{Address: address, Access: resources.NewNativeNetworkAccess()}}}
	}
	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)
	manager = manager.ForConsumerModule("payments", declared)
	manager.WithLoader(loader).
		WithNetworkMappings([]*basev0.NetworkMapping{mapping("hidden", "http://localhost:1"), mapping("primary", "http://localhost:2"), mapping("secondary", "http://localhost:3")}, resources.NewNativeNetworkAccess()).
		WithRunProducers(func(unique string) bool { return unique == "platform/authority" })
	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	_, err = manager.GetCompositionRootWorkspaceConfigurations(ctx)
	require.ErrorIs(t, err, resources.ErrAmbiguousEndpointReference)
	require.Contains(t, err.Error(), "work-context/addresses")
}
