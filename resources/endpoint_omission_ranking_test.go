package resources_test

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func authorityConf(key, value string) *basev0.Configuration {
	return &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{{
		Name: "authority", ConfigurationValues: []*basev0.ConfigurationValue{{Key: key, Value: value}},
	}}}
}

func authorityTemplated(key string, literals ...string) *basev0.Configuration {
	var segments []*basev0.ConfigurationValueTemplateSegment
	for _, literal := range literals {
		segments = append(segments, &basev0.ConfigurationValueTemplateSegment{Content: &basev0.ConfigurationValueTemplateSegment_Literal{Literal: literal}})
	}
	return &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{{
		Name: "authority", ConfigurationValues: []*basev0.ConfigurationValue{{Key: key, Template: &basev0.ConfigurationValueTemplate{Segments: segments}}},
	}}}
}

// In the API fallback, an invalid declaration among the candidates is a fault
// reported before any sibling is selected or any omission allowed — whatever
// the declaration order, and even when a public sibling could have answered.
func TestAnInvalidDeclarationAmongAPICandidatesIsNeverDiscarded(t *testing.T) {
	ctx := context.Background()
	inRun := resources.WithRunProducers(func(unique string) bool { return unique == selectionUnique })
	reference := &resources.EndpointInformation{Module: selectionModule, Service: selectionService, Name: "grpc"}
	hidden := selectionEndpoint("hidden", "grpc", "private")
	broken := selectionEndpoint("broken", "grpc", "pubilc")
	public := selectionEndpoint("open", "grpc", "public")
	mappings := []*basev0.NetworkMapping{
		selectionMapping("hidden", "grpc", nativeAt("http://localhost:1")),
		selectionMapping("broken", "grpc", nativeAt("http://localhost:2")),
		selectionMapping("open", "grpc", nativeAt("http://localhost:3")),
	}
	for name, declared := range map[string][]*resources.Endpoint{
		"private then invalid":          {hidden, broken},
		"invalid then private":          {broken, hidden},
		"public sibling then invalid":   {public, broken},
		"invalid then public sibling":   {broken, public},
		"private, public, then invalid": {hidden, public, broken},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resources.SelectEndpointForReference("payments", reference, declared)
			require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration)
			require.NotErrorIs(t, err, resources.ErrEndpointNotReachable)

			consumer := resources.WithConsumer("payments", declaredBy(declared...))
			ref := "${endpoint:" + selectionUnique + "/grpc}"
			for kind, conf := range map[string]*basev0.Configuration{
				"plain":     authorityConf("address", ref),
				"templated": authorityTemplated("address", "grpc://"+ref),
			} {
				_, err := resources.InterpolateRunWideConfigurationEndpoints(ctx, conf, mappings, resources.NewNativeNetworkAccess(), inRun, consumer)
				require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration, kind)
				require.Contains(t, err.Error(), "authority/address", kind)
				require.NotContains(t, err.Error(), "localhost:3", kind+": the public sibling must not stand in")
			}
		})
	}
}

// The strict path may drop only an unavailable endpoint of a producer outside
// the run. A value carrying such a reference beside a denial is refused with the
// denial — whichever came first, plain or templated — because the worse of the
// two omission classes is what the value reports.
func TestAStrictReadReportsADenialOverAnUnavailabilityWhateverTheOrder(t *testing.T) {
	ctx := context.Background()
	outside := resources.WithRunProducers(func(string) bool { return false })
	consumer := resources.WithConsumer("payments", declaredBy(
		selectionEndpoint("missing", "grpc", "public"),
		selectionEndpoint("hidden", "grpc", "private"),
	))
	missing := "${endpoint:" + selectionUnique + "/missing}"
	hidden := "${endpoint:" + selectionUnique + "/hidden}"
	for name, conf := range map[string]*basev0.Configuration{
		"missing then hidden":             authorityConf("addresses", missing+","+hidden),
		"hidden then missing":             authorityConf("addresses", hidden+","+missing),
		"missing literal, hidden literal": authorityTemplated("addresses", missing, hidden),
		"hidden literal, missing literal": authorityTemplated("addresses", hidden, missing),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resources.InterpolateConfigurationEndpoints(ctx, conf, nil, resources.NewNativeNetworkAccess(), outside, consumer)
			require.ErrorIs(t, err, resources.ErrEndpointNotReachable)
			require.Contains(t, err.Error(), "authority/addresses")
		})
	}
	// The control: the unavailable out-of-run reference alone is dropped.
	resolved, err := resources.InterpolateConfigurationEndpoints(ctx, authorityConf("addresses", missing), nil, resources.NewNativeNetworkAccess(), outside, consumer)
	require.NoError(t, err)
	require.Empty(t, resolved.GetInfos()[0].GetConfigurationValues())
}

// The declaration is judged before the consumer is: the producer's own module
// does not make an unsupported visibility value valid, with or without a
// mapping, by the validator and by the resolution.
func TestTheOwningModuleDoesNotBypassAnInvalidDeclaration(t *testing.T) {
	ctx := context.Background()
	err := resources.ValidateEndpointVisibility(selectionModule, selectionModule, selectionService, "grpc", "pubilc", nil)
	require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration)
	require.NoError(t, resources.ValidateEndpointVisibility(selectionModule, selectionModule, selectionService, "grpc", "private", nil))

	inRun := resources.WithRunProducers(func(unique string) bool { return unique == selectionUnique })
	own := resources.WithConsumer(selectionModule, declaredBy(selectionEndpoint("grpc", "grpc", "pubilc")))
	ref := "${endpoint:" + selectionUnique + "/grpc}"
	for name, mappings := range map[string][]*basev0.NetworkMapping{
		"with a mapping":    {selectionMapping("grpc", "grpc", nativeAt("http://localhost:1111"))},
		"without a mapping": nil,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resources.InterpolateRunWideConfigurationEndpoints(ctx, authorityConf("address", ref), mappings, resources.NewNativeNetworkAccess(), inRun, own)
			require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration)
			require.Contains(t, err.Error(), "authority/address")
		})
	}
}

// A producer the workspace does not declare is a composition fault on the
// run-wide path too — never an omission, whatever the run set says.
func TestAnUnknownProducerIsRefusedOnTheRunWidePath(t *testing.T) {
	ctx := context.Background()
	inRun := resources.WithRunProducers(func(string) bool { return true })
	unknown := resources.WithConsumer("payments", func(string) ([]*resources.Endpoint, bool) { return nil, false })
	for name, conf := range map[string]*basev0.Configuration{
		"plain":     authorityConf("address", "${endpoint:nowhere/x/grpc}"),
		"templated": authorityTemplated("address", "grpc://${endpoint:nowhere/x/grpc}"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resources.InterpolateRunWideConfigurationEndpoints(ctx, conf, nil, resources.NewNativeNetworkAccess(), inRun, unknown)
			require.ErrorIs(t, err, resources.ErrUnknownProducer)
			require.Contains(t, err.Error(), "authority/address")
		})
	}
}

// When every API match exists but none is reachable, the refusal carries the
// visibility reason — the consumer is told it may not reach the endpoint, not
// that the producer declares none.
func TestAllUnreachableAPIMatchesRefuseWithTheVisibilityReason(t *testing.T) {
	_, err := resources.SelectEndpointForReference("payments",
		&resources.EndpointInformation{Module: selectionModule, Service: selectionService, Name: "grpc"},
		[]*resources.Endpoint{selectionEndpoint("hidden", "grpc", "private"), selectionEndpoint("inner", "grpc", "internal")})
	require.ErrorIs(t, err, resources.ErrEndpointNotReachable)
	require.NotErrorIs(t, err, resources.ErrNoSuchEndpoint)
	require.Contains(t, err.Error(), "private to module")
}
