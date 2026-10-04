package resources_test

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// The cases round one of core#702's review named as missing or doubted, each
// against the one implementation.

// A qualifier is a statement about the endpoint NAMED, not a request for any
// sibling serving that API. `${…/grpc::http}` on a producer declaring `grpc`
// (api grpc) and `admin` (api http) used to drop the named endpoint at the API
// filter and let `admin` answer — the named endpoint was never judged.
func TestAnAPIQualifierNeverTurnsAnExactNameIntoASibling(t *testing.T) {
	declared := []*resources.Endpoint{
		selectionEndpoint("grpc", "grpc", "private"),
		selectionEndpoint("admin", "http", "public"),
	}
	_, err := resources.SelectEndpointForReference("payments",
		&resources.EndpointInformation{Module: selectionModule, Service: selectionService, Name: "grpc", API: "http"}, declared)
	require.ErrorIs(t, err, resources.ErrEndpointAPIMismatch)
	require.Contains(t, err.Error(), `"grpc"`)
	require.NotContains(t, err.Error(), "admin")

	// A qualifier that agrees selects the named endpoint — and is then judged
	// on visibility like any exact name.
	_, err = resources.SelectEndpointForReference("payments",
		&resources.EndpointInformation{Module: selectionModule, Service: selectionService, Name: "grpc", API: "grpc"}, declared)
	require.ErrorIs(t, err, resources.ErrEndpointNotReachable)
	selected, err := resources.SelectEndpointForReference(selectionModule,
		&resources.EndpointInformation{Module: selectionModule, Service: selectionService, Name: "grpc", API: "grpc"}, declared)
	require.NoError(t, err)
	require.True(t, selected.ExactName)
}

// The mapping bound is the endpoint that was declared and selected: a mapping
// published under the selected name that states another API is conflicting
// metadata and is refused, never bound; a mapping that states no API is not in
// conflict.
func TestBindingHoldsTheMappingToTheSelectedAPI(t *testing.T) {
	selection := resources.EndpointSelectionContext{ConsumerModule: "payments",
		Declared: declaredBy(selectionEndpoint("rpc", "grpc", "public"))}

	_, err := resolveFor(t, selectionUnique+"/rpc", []*basev0.NetworkMapping{
		selectionMapping("rpc", "http", nativeAt("http://localhost:8080")),
		selectionMapping("rpc", "grpc", nativeAt("http://localhost:9090")),
	}, resources.NewNativeNetworkAccess(), selection)
	require.Error(t, err, "a mapping of the selected name with another API is conflicting metadata")
	require.Contains(t, err.Error(), "conflicting mapping metadata")
	require.NotContains(t, err.Error(), "localhost:8080")

	out, err := resolveFor(t, selectionUnique+"/rpc::grpc", []*basev0.NetworkMapping{
		selectionMapping("rpc", "", nativeAt("http://localhost:9090")),
	}, resources.NewNativeNetworkAccess(), selection)
	require.NoError(t, err, "a mapping stating no API is the declared endpoint")
	require.Equal(t, "http://localhost:9090", out)
}

// A producer the workspace does not declare is a composition fault, and a
// producer that declares no endpoints is a different one; neither is "not
// available to this consumer", which the run-wide path would drop.
func TestAnUnknownProducerAndAnEmptyManifestAreDistinctRefusals(t *testing.T) {
	unknown := resources.EndpointSelectionContext{ConsumerModule: "payments",
		Declared: func(string) ([]*resources.Endpoint, bool) { return nil, false }}
	_, err := resolveFor(t, selectionUnique+"/grpc", nil, resources.NewNativeNetworkAccess(), unknown)
	require.ErrorIs(t, err, resources.ErrUnknownProducer)

	empty := resources.EndpointSelectionContext{ConsumerModule: "payments",
		Declared: func(string) ([]*resources.Endpoint, bool) { return nil, true }}
	_, err = resolveFor(t, selectionUnique+"/grpc", nil, resources.NewNativeNetworkAccess(), empty)
	require.ErrorIs(t, err, resources.ErrNoSuchEndpoint)
	require.Contains(t, err.Error(), "declares no endpoints at all")
}

// Two references into one producer in one configuration: each is answered on
// its own, and neither keeps a mapping alive for the other. This is the case
// a consumer of this package could not get right by ordering the mappings it
// handed over, because the two references need opposite orders.
func TestTwoReferencesIntoOneProducerAreEachAnsweredByTheEndpointTheyName(t *testing.T) {
	conf := &basev0.Configuration{
		Origin: resources.ConfigurationWorkspace,
		Infos: []*basev0.ConfigurationInformation{{
			Name: "authority",
			ConfigurationValues: []*basev0.ConfigurationValue{
				{Key: "primary", Value: "${endpoint:" + selectionUnique + "/grpc}"},
				{Key: "secondary", Value: "${endpoint:" + selectionUnique + "/admin}"},
			},
		}},
	}
	for name, mappings := range map[string][]*basev0.NetworkMapping{
		"admin first": {
			selectionMapping("admin", "grpc", nativeAt("http://localhost:2222")),
			selectionMapping("grpc", "grpc", nativeAt("http://localhost:1111")),
		},
		"grpc first": {
			selectionMapping("grpc", "grpc", nativeAt("http://localhost:1111")),
			selectionMapping("admin", "grpc", nativeAt("http://localhost:2222")),
		},
	} {
		t.Run(name, func(t *testing.T) {
			resolved, err := resources.InterpolateConfigurationEndpoints(context.Background(), conf, mappings, resources.NewNativeNetworkAccess(),
				resources.WithConsumer("payments", publicTrio().Declared))
			require.NoError(t, err)
			values := resolved.GetInfos()[0].GetConfigurationValues()
			require.Equal(t, "http://localhost:1111", values[0].GetValue(), "primary names grpc")
			require.Equal(t, "http://localhost:2222", values[1].GetValue(), "secondary names admin")
		})
	}
}

// The run-wide path drops a value only when the endpoint is not available to
// this consumer or this consumer may not reach it. A composition fault — here an
// ambiguous API reference, which would be the same fault for every consumer —
// is a refusal, not a key silently missing from a delivered configuration.
func TestTheRunWidePathRefusesACompositionFaultInsteadOfDroppingTheKey(t *testing.T) {
	ctx := context.Background()
	inRun := resources.WithRunProducers(func(unique string) bool { return unique == selectionUnique })
	mappings := []*basev0.NetworkMapping{
		selectionMapping("grpc", "grpc", nativeAt("http://localhost:1111")),
		selectionMapping("admin", "grpc", nativeAt("http://localhost:2222")),
	}
	ambiguous := &basev0.Configuration{
		Origin: resources.ConfigurationWorkspace,
		Infos: []*basev0.ConfigurationInformation{{
			Name:                "authority",
			ConfigurationValues: []*basev0.ConfigurationValue{{Key: "address", Value: "${endpoint:" + selectionUnique + "/rest}"}},
		}},
	}
	// `rest` names no endpoint: the API matches are all three public siblings.
	ambiguousTrio := resources.WithConsumer("payments", declaredBy(
		selectionEndpoint("grpc", "rest", "public"),
		selectionEndpoint("admin", "rest", "public"),
	))
	_, err := resources.InterpolateRunWideConfigurationEndpoints(ctx, ambiguous, mappings, resources.NewNativeNetworkAccess(), inRun, ambiguousTrio)
	require.ErrorIs(t, err, resources.ErrAmbiguousEndpointReference)
	require.Contains(t, err.Error(), "authority/address")

	// And a template literal carrying the same reference is refused the same
	// way: the fault does not hide inside an assembly.
	templated := &basev0.Configuration{
		Origin: resources.ConfigurationWorkspace,
		Infos: []*basev0.ConfigurationInformation{{
			Name: "authority",
			ConfigurationValues: []*basev0.ConfigurationValue{{Key: "dsn", Template: &basev0.ConfigurationValueTemplate{
				Segments: []*basev0.ConfigurationValueTemplateSegment{{Content: &basev0.ConfigurationValueTemplateSegment_Literal{Literal: "grpc://${endpoint:" + selectionUnique + "/rest}/x"}}},
			}}},
		}},
	}
	_, err = resources.InterpolateRunWideConfigurationEndpoints(ctx, templated, mappings, resources.NewNativeNetworkAccess(), inRun, ambiguousTrio)
	require.ErrorIs(t, err, resources.ErrAmbiguousEndpointReference)

	// Whereas an endpoint this consumer may not reach IS a per-consumer
	// omission: the value was not for it, and nothing else of the
	// configuration is lost.
	private := &basev0.Configuration{
		Origin: resources.ConfigurationWorkspace,
		Infos: []*basev0.ConfigurationInformation{{
			Name: "authority",
			ConfigurationValues: []*basev0.ConfigurationValue{
				{Key: "address", Value: "${endpoint:" + selectionUnique + "/grpc}"},
				{Key: "public", Value: "${endpoint:" + selectionUnique + "/admin}"},
			},
		}},
	}
	privateGrpc := resources.WithConsumer("payments", declaredBy(
		selectionEndpoint("grpc", "grpc", "private"),
		selectionEndpoint("admin", "grpc", "public"),
	))
	resolved, err := resources.InterpolateRunWideConfigurationEndpoints(ctx, private, mappings, resources.NewNativeNetworkAccess(), inRun, privateGrpc)
	require.NoError(t, err)
	values := resolved.GetInfos()[0].GetConfigurationValues()
	require.Len(t, values, 1)
	require.Equal(t, "public", values[0].GetKey())
	require.Equal(t, "http://localhost:2222", values[0].GetValue())
}
