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
	type found struct {
		consumer, key string
		position      int
	}
	var got []found
	for _, reference := range unresolved.References {
		got = append(got, found{reference.Consumer, reference.Key, reference.Position})
		require.Equal(t, "assistant", reference.Group)
	}
	require.Equal(t, []found{
		{"assistant/chat", "documents-endpoint", 1},
		{"assistant/chat", "host-missing", 1},
		{"assistant/worker", "documents-endpoint", 1},
		{"assistant/worker", "host-missing", 1},
	}, got)
	// The report names the key and the position, and what the manifest says;
	// never the reference's text, which is text from a value.
	require.Contains(t, err.Error(), "assistant/chat: assistant/documents-endpoint, reference 1: ")
	require.NotContains(t, err.Error(), "documents/store/grpc")

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

// The plan-time report never carries text from a value: a reference that did
// not validate as coordinates is named by its key only.
func TestCheckEndpointReferencesNeverEchoesAValue(t *testing.T) {
	const secret = "syntheticsecret9c1e"
	consumer := referenceCheckService("assistant", "chat", []string{"assistant"})
	for name, value := range map[string]string{
		"a marker body that is the secret":         "${endpoint:credential=" + secret + "}",
		"beside an empty marker":                   "${endpoint:};${endpoint:credential=" + secret + "}",
		"a bad api qualifier":                      "${endpoint:assistant/chat/grpc::" + secret + "}",
		"a secret that is a valid name":            "${endpoint:assistant/chat/" + secret + "}",
		"a secret that is a valid producer":        "${endpoint:" + secret + "/chat/grpc}",
		"a private endpoint under a secret module": "${endpoint:" + secret + "/chat/grpc}",
		"an empty api qualifier":                   "${endpoint:assistant/chat/grpc::}",
	} {
		t.Run(name, func(t *testing.T) {
			err := configurations.CheckEndpointReferences([]*basev0.ConfigurationInformation{
				{Name: "assistant", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "bad", Secret: true, Value: value}}},
			}, []*resources.Service{consumer}, resources.RunProfile{}, func(string) (*resources.Service, bool) { return nil, false })
			require.Error(t, err)
			require.NotContains(t, err.Error(), secret)
			require.Contains(t, err.Error(), "assistant/bad")
		})
	}
	// With a real producer, so the reference reaches selection: a private
	// module-less declaration scoped under the secret module, an unknown
	// endpoint, a bad qualifier — the refusal names none of the value.
	anyProducer := referenceCheckService("x", "y", nil)
	anyProducer.Endpoints = []*resources.Endpoint{{Name: "grpc", API: "grpc", Visibility: resources.VisibilityPrivate}}
	for name, value := range map[string]string{
		"private under a secret module": "${endpoint:" + secret + "/chat/grpc}",
		"a secret endpoint name":        "${endpoint:assistant/chat/" + secret + "}",
		"an empty api qualifier":        "${endpoint:assistant/chat/grpc::}",
	} {
		t.Run(name, func(t *testing.T) {
			err := configurations.CheckEndpointReferences([]*basev0.ConfigurationInformation{
				{Name: "assistant", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "bad", Secret: true, Value: value}}},
			}, []*resources.Service{consumer}, resources.RunProfile{}, func(string) (*resources.Service, bool) { return anyProducer, true })
			require.Error(t, err)
			require.NotContains(t, err.Error(), secret)
			require.Contains(t, err.Error(), "assistant/bad")
		})
	}
	err := configurations.CheckEndpointReferences([]*basev0.ConfigurationInformation{
		{Name: "assistant", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "bad", Value: "${endpoint:assistant/chat/grpc::}"}}},
	}, []*resources.Service{consumer}, resources.RunProfile{}, func(string) (*resources.Service, bool) { return anyProducer, true })
	require.ErrorContains(t, err, "malformed reference", "an empty qualifier is malformed, not an unknown endpoint")
}

// A marker the reference grammar cannot read fails the plan, not just the
// render: in the value and in a template literal alike.
func TestCheckEndpointReferencesReportsAMalformedMarker(t *testing.T) {
	consumer := referenceCheckService("assistant", "chat", []string{"assistant"})
	for name, value := range map[string]*basev0.ConfigurationValue{
		"empty marker":        {Key: "bad", Value: "${endpoint:}"},
		"unterminated marker": {Key: "bad", Value: "${endpoint:assistant/chat/grpc"},
		"in a template literal": {Key: "bad", Template: &basev0.ConfigurationValueTemplate{
			Segments: []*basev0.ConfigurationValueTemplateSegment{
				{Content: &basev0.ConfigurationValueTemplateSegment_Literal{Literal: "x=${endpoint:"}},
			},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			err := configurations.CheckEndpointReferences([]*basev0.ConfigurationInformation{
				{Name: "assistant", ConfigurationValues: []*basev0.ConfigurationValue{value}},
			}, []*resources.Service{consumer}, resources.RunProfile{}, func(string) (*resources.Service, bool) { return nil, false })
			var unresolved *configurations.UnresolvedReferencesError
			require.True(t, errors.As(err, &unresolved), "got %v", err)
			require.Len(t, unresolved.References, 1)
			require.Equal(t, "bad", unresolved.References[0].Key)
			require.Contains(t, unresolved.References[0].Reason, "malformed reference")
		})
	}
}

// A reference is subject to the producer's export boundary: declaring the group
// instead of the dependency must not be a way around endpoint visibility. The
// same endpoint is fine for a consumer in the producer's own module, and an
// internal endpoint — which names nobody — is fine for whatever composes the
// workspace.
func TestCheckEndpointReferencesRefusesAnEndpointTheConsumerModuleMayNotReceive(t *testing.T) {
	private := referenceCheckService("host", "api", nil, &resources.Endpoint{Name: "grpc", API: "grpc"})
	internal := referenceCheckService("host", "gate", nil,
		&resources.Endpoint{Name: "grpc", API: "grpc", Visibility: resources.VisibilityInternal})
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
	require.Len(t, unresolved.References, 1, "the internal gate resolves for the outsider; only the private api is refused")
	require.Equal(t, "host-endpoint", unresolved.References[0].Key)
	require.Contains(t, unresolved.References[0].Reason, `is private to module "host"`)

	require.NoError(t, configurations.CheckEndpointReferences(provided, []*resources.Service{sibling}, resources.RunProfile{}, lookup),
		"a consumer in the producer's own module receives both")
	gateOnly := []*basev0.ConfigurationInformation{
		{Name: "platform", ConfigurationValues: []*basev0.ConfigurationValue{
			{Key: "gate-endpoint", Value: "${endpoint:host/gate/grpc}"},
		}},
	}
	for _, consumer := range []*resources.Service{allowed, outsider} {
		require.NoError(t, configurations.CheckEndpointReferences(gateOnly, []*resources.Service{consumer}, resources.RunProfile{}, lookup),
			"an internal endpoint is reachable by whatever composes the workspace")
	}
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
		resources.WithRunProducers(func(string) bool { return false }),
		resources.WithConsumer("assistant", resources.DeclaredEndpointsOf([]*resources.Service{temporal})))
	require.NoError(t, err, "a run without the producer drops the value")
	require.Empty(t, dropped.Infos[0].GetConfigurationValues())

	_, err = resources.InterpolateConfigurationEndpoints(ctx, conf, nil, resources.NewNativeNetworkAccess(),
		resources.WithRunProducers(func(unique string) bool { return unique == "infra/temporal" }))
	require.Error(t, err, "a run WITH the producer must resolve it")
	require.Contains(t, err.Error(), "platform/temporal-address")
}

// A producer's ${endpoint:…} written inside a ConfigurationValueTemplate literal
// must be checked like any other. This check exists to refuse a plan before
// anything is built or started; reading only value.Value let a templated value's
// reference through silently, which is the failure it was added to remove.
func TestCheckEndpointReferencesSeesTemplateLiterals(t *testing.T) {
	consumer := referenceCheckService("mod", "app", []string{"postgres"})
	lookup := func(unique string) (*resources.Service, bool) {
		if unique == "mod/app" {
			return consumer, true
		}
		return nil, false
	}
	provided := []*basev0.ConfigurationInformation{{
		Name: "postgres",
		ConfigurationValues: []*basev0.ConfigurationValue{
			{Key: "connection", Secret: true, Template: &basev0.ConfigurationValueTemplate{
				Segments: []*basev0.ConfigurationValueTemplateSegment{
					{Content: &basev0.ConfigurationValueTemplateSegment_Literal{
						Literal: "postgresql://reader@${endpoint:absent/store/postgres}/app"}},
				},
			}},
		},
	}}
	err := configurations.CheckEndpointReferences(provided, []*resources.Service{consumer}, resources.RunProfile{}, lookup)
	var unresolved *configurations.UnresolvedReferencesError
	require.True(t, errors.As(err, &unresolved), "a reference in a template literal must fail the plan check, got %v", err)
	require.Len(t, unresolved.References, 1)
	require.Equal(t, "connection", unresolved.References[0].Key)
	require.Equal(t, "postgres", unresolved.References[0].Group)
}

// The checker's verdict does not depend on manifest order: with the public API
// sibling declared FIRST — the order the old first-match scan accepted — an
// exact name the consumer may not reach is still refused, with the visibility
// reason, by the real checker.
func TestCheckEndpointReferencesRefusesAPrivateExactNameWhateverTheManifestOrder(t *testing.T) {
	for name, endpoints := range map[string][]*resources.Endpoint{
		"public sibling first": {
			{Name: "admin", API: "grpc", Visibility: resources.VisibilityPublic},
			{Name: "grpc", API: "grpc", Visibility: resources.VisibilityPrivate},
		},
		"private exact name first": {
			{Name: "grpc", API: "grpc", Visibility: resources.VisibilityPrivate},
			{Name: "admin", API: "grpc", Visibility: resources.VisibilityPublic},
		},
	} {
		t.Run(name, func(t *testing.T) {
			authority := referenceCheckService("platform", "authority", nil, endpoints...)
			consumer := referenceCheckService("payments", "worker", []string{"work-context"})
			plan := map[string]*resources.Service{"platform/authority": authority, "payments/worker": consumer}
			lookup := func(unique string) (*resources.Service, bool) { service, ok := plan[unique]; return service, ok }
			provided := []*basev0.ConfigurationInformation{{Name: "work-context", ConfigurationValues: []*basev0.ConfigurationValue{
				{Key: "authority-address", Value: "${endpoint:platform/authority/grpc}"},
			}}}
			err := configurations.CheckEndpointReferences(provided, []*resources.Service{consumer}, resources.RunProfile{}, lookup)
			require.Error(t, err)
			require.Contains(t, err.Error(), "private to module")
			require.NotContains(t, err.Error(), "admin", "the sibling is never judged in the named endpoint's place")
		})
	}
}
