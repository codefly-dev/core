package configurations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func referenceCheckService(module, name string, groups []string, endpoints ...*resources.Endpoint) *resources.Service {
	service := &resources.Service{Name: name, WorkspaceConfigurationDependencies: groups, Endpoints: endpoints}
	service.WithModule(module)
	for _, endpoint := range endpoints {
		endpoint.Module, endpoint.Service = module, name
	}
	return service
}

// excludingGroups is the run profile a caller resolved, carrying the groups its
// consumers never receive.
func excludingGroups(groups ...string) resources.RunProfile {
	return resources.RunProfile{ExcludeWorkspaceConfigurations: groups}
}

// Every unresolvable reference in the groups the plan's consumers declare is
// reported at once — consumer, group, key, reference and producer — while a
// resolvable one, an excluded group and a group no consumer declares are not.
func TestCheckEndpointReferencesListsEveryUnresolvedReference(t *testing.T) {
	host := referenceCheckService("host", "api", nil,
		&resources.Endpoint{Name: "http", API: "http", Visibility: resources.VisibilityPublic},
		&resources.Endpoint{Name: "rpc", API: "grpc", Visibility: resources.VisibilityPublic})
	chat := referenceCheckService("assistant", "chat", []string{"assistant", "excluded"})
	worker := referenceCheckService("assistant", "worker", []string{"assistant"})
	plan := map[string]*resources.Service{"host/api": host, "assistant/chat": chat, "assistant/worker": worker}
	lookup := func(unique string) (*resources.Service, bool) { service, ok := plan[unique]; return service, ok }

	provided := []*basev0.ConfigurationInformation{
		{Name: "assistant", ConfigurationValues: []*basev0.ConfigurationValue{
			{Key: "host-endpoint", Value: "${endpoint:host/api/http}"},
			{Key: "host-rpc", Value: "${endpoint:host/api/grpc|authority}"},
			{Key: "documents-endpoint", Value: "${endpoint:documents/store/grpc}"},
			{Key: "host-missing", Value: "${endpoint:host/api/admin}"},
		}},
		{Name: "excluded", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "k", Value: "${endpoint:absent/service/http}"}}},
		{Name: "undeclared", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "k", Value: "${endpoint:absent/service/http}"}}},
	}
	err := configurations.CheckEndpointReferences(provided, []*resources.Service{worker, chat}, excludingGroups("excluded"), lookup)
	var unresolved *configurations.UnresolvedReferencesError
	require.True(t, errors.As(err, &unresolved), "got %v", err)
	type found struct{ consumer, key, producer string }
	var got []found
	for _, reference := range unresolved.References {
		got = append(got, found{reference.Consumer, reference.Key, reference.Producer})
		require.Equal(t, "assistant", reference.Group)
	}
	require.Equal(t, []found{
		{"assistant/chat", "documents-endpoint", "documents/store"},
		{"assistant/chat", "host-missing", "host/api"},
		{"assistant/worker", "documents-endpoint", "documents/store"},
		{"assistant/worker", "host-missing", "host/api"},
	}, got)
	require.Contains(t, err.Error(), "assistant/chat: assistant/documents-endpoint = ${endpoint:documents/store/grpc} (producer documents/store)")

	require.NoError(t, configurations.CheckEndpointReferences(provided[:1], []*resources.Service{host}, resources.RunProfile{}, lookup),
		"a service that does not declare the group is not checked against it")
}

// A reference that cannot name an endpoint is reported as malformed.
func TestCheckEndpointReferencesReportsAMalformedReference(t *testing.T) {
	consumer := referenceCheckService("assistant", "chat", []string{"assistant"})
	err := configurations.CheckEndpointReferences([]*basev0.ConfigurationInformation{
		{Name: "assistant", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "bad", Value: "${endpoint:host}"}}},
	}, []*resources.Service{consumer}, resources.RunProfile{}, func(string) (*resources.Service, bool) { return nil, false })
	require.Error(t, err)
	require.Contains(t, err.Error(), "assistant/bad")
	require.Contains(t, err.Error(), "malformed reference")
}

// A reference is subject to the producer's export boundary: declaring the group
// instead of the dependency must not be a way around endpoint visibility. The
// same endpoint is fine for a consumer in the producer's own module.
func TestCheckEndpointReferencesRefusesAnEndpointTheConsumerModuleMayNotReceive(t *testing.T) {
	private := referenceCheckService("host", "api", nil, &resources.Endpoint{Name: "grpc", API: "grpc"})
	internal := referenceCheckService("host", "gate", nil,
		&resources.Endpoint{Name: "grpc", API: "grpc", Visibility: resources.VisibilityInternal, AllowModules: []string{"billing"}})
	outsider := referenceCheckService("assistant", "chat", []string{"platform"})
	sibling := referenceCheckService("host", "worker", []string{"platform"})
	allowed := referenceCheckService("billing", "ledger", []string{"platform"})
	plan := map[string]*resources.Service{
		"host/api": private, "host/gate": internal,
		"assistant/chat": outsider, "host/worker": sibling, "billing/ledger": allowed,
	}
	lookup := func(unique string) (*resources.Service, bool) { service, ok := plan[unique]; return service, ok }
	provided := []*basev0.ConfigurationInformation{
		{Name: "platform", ConfigurationValues: []*basev0.ConfigurationValue{
			{Key: "host-endpoint", Value: "${endpoint:host/api/grpc}"},
			{Key: "gate-endpoint", Value: "${endpoint:host/gate/grpc}"},
		}},
	}

	err := configurations.CheckEndpointReferences(provided, []*resources.Service{outsider}, resources.RunProfile{}, lookup)
	var unresolved *configurations.UnresolvedReferencesError
	require.True(t, errors.As(err, &unresolved), "got %v", err)
	require.Len(t, unresolved.References, 2)
	// Ordered by key: gate-endpoint (internal, not allowing assistant) then
	// host-endpoint (private to host).
	require.Contains(t, unresolved.References[0].Reason, `does not permit module "assistant"`)
	require.Contains(t, unresolved.References[1].Reason, `is private to module "host"`)

	require.NoError(t, configurations.CheckEndpointReferences(provided, []*resources.Service{sibling}, resources.RunProfile{}, lookup),
		"a consumer in the producer's own module receives both")
	gateOnly := []*basev0.ConfigurationInformation{
		{Name: "platform", ConfigurationValues: []*basev0.ConfigurationValue{
			{Key: "gate-endpoint", Value: "${endpoint:host/gate/grpc}"},
		}},
	}
	require.NoError(t, configurations.CheckEndpointReferences(gateOnly, []*resources.Service{allowed}, resources.RunProfile{}, lookup),
		"an allow-modules entry is honored")
}

// The two halves of the contract do not subsume one another, and the plan-time
// half must not refuse what only a run can answer: a reference to a real
// producer of the workspace passes the composition check whatever run is about
// to happen, and it is the resolve-time half that reports it when a run that
// DOES contain that producer failed to hand the consumer its address.
func TestCheckEndpointReferencesLeavesTheRunSetToResolveTime(t *testing.T) {
	ctx := context.Background()
	temporal := referenceCheckService("infra", "temporal", nil,
		&resources.Endpoint{Name: "grpc", API: "grpc", Visibility: resources.VisibilityPublic})
	consumer := referenceCheckService("assistant", "chat", []string{"platform"})
	plan := map[string]*resources.Service{"infra/temporal": temporal, "assistant/chat": consumer}
	lookup := func(unique string) (*resources.Service, bool) { service, ok := plan[unique]; return service, ok }
	provided := []*basev0.ConfigurationInformation{
		{Name: "platform", ConfigurationValues: []*basev0.ConfigurationValue{
			{Key: "temporal-address", Value: "${endpoint:infra/temporal/grpc}"},
		}},
	}
	require.NoError(t, configurations.CheckEndpointReferences(provided, []*resources.Service{consumer}, resources.RunProfile{}, lookup),
		"a producer of the workspace is never a composition fault, whatever the run excludes")

	conf := &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: provided}
	dropped, err := resources.InterpolateConfigurationEndpoints(ctx, conf, nil, resources.NewNativeNetworkAccess(),
		resources.WithRunProducers(func(string) bool { return false }))
	require.NoError(t, err, "a run without the producer drops the value")
	require.Empty(t, dropped.Infos[0].GetConfigurationValues())

	_, err = resources.InterpolateConfigurationEndpoints(ctx, conf, nil, resources.NewNativeNetworkAccess(),
		resources.WithRunProducers(func(unique string) bool { return unique == "infra/temporal" }))
	require.Error(t, err, "a run WITH the producer must resolve it")
	require.Contains(t, err.Error(), "platform/temporal-address")
}
