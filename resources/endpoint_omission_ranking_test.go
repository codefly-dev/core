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

// The reserved `${endpoint:` prefix in a form the grammar does not match — an
// empty marker, an unterminated one — is a fault wherever it is met: never
// delivered unchanged, never omitted behind a reference this consumer merely
// cannot resolve, plain or templated.
func TestAMalformedEndpointMarkerIsRefusedNotDeliveredOrOmitted(t *testing.T) {
	ctx := context.Background()
	inRun := resources.WithRunProducers(func(unique string) bool { return unique == selectionUnique })
	consumer := resources.WithConsumer("payments", declaredBy(selectionEndpoint("hidden", "grpc", "private")))
	hidden := "${endpoint:" + selectionUnique + "/hidden}"
	for name, conf := range map[string]*basev0.Configuration{
		"empty marker alone":              authorityConf("address", "${endpoint:}"),
		"unterminated marker alone":       authorityConf("address", "${endpoint:"+selectionUnique+"/grpc"),
		"empty marker beside an omission": authorityConf("address", "${endpoint:},"+hidden),
		"omission beside an empty marker": authorityConf("address", hidden+",${endpoint:}"),
		"templated empty marker":          authorityTemplated("address", "x=${endpoint:}"),
		"templated beside an omission":    authorityTemplated("address", hidden, "y=${endpoint:}"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resources.InterpolateRunWideConfigurationEndpoints(ctx, conf, nil, resources.NewNativeNetworkAccess(), inRun, consumer)
			require.ErrorIs(t, err, resources.ErrMalformedEndpointReference)
			require.Contains(t, err.Error(), "authority/address")
			_, err = resources.InterpolateConfigurationEndpoints(ctx, conf, nil, resources.NewNativeNetworkAccess(), inRun, consumer)
			require.ErrorIs(t, err, resources.ErrMalformedEndpointReference)
		})
	}
	out, err := resources.InterpolateEndpointsFor(ctx, "${endpoint:}", nil, resources.NewNativeNetworkAccess(), resources.EndpointSelectionContext{ConsumerModule: "payments", Declared: declaredBy()})
	require.ErrorIs(t, err, resources.ErrMalformedEndpointReference)
	require.Empty(t, out)
}

// Conflicting mapping metadata is refused whichever mapping was published
// first: every mapping of the selected endpoint is judged before any is bound.
func TestConflictingMappingMetadataIsRefusedInEitherOrder(t *testing.T) {
	selection := resources.EndpointSelectionContext{ConsumerModule: "payments",
		Declared: declaredBy(selectionEndpoint("rpc", "grpc", "public"))}
	for name, mappings := range map[string][]*basev0.NetworkMapping{
		"conflict first": {selectionMapping("rpc", "http", nativeAt("http://localhost:8080")), selectionMapping("rpc", "grpc", nativeAt("http://localhost:9090"))},
		"conflict last":  {selectionMapping("rpc", "grpc", nativeAt("http://localhost:9090")), selectionMapping("rpc", "http", nativeAt("http://localhost:8080"))},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := resolveFor(t, selectionUnique+"/rpc", mappings, resources.NewNativeNetworkAccess(), selection)
			require.Error(t, err)
			require.Contains(t, err.Error(), "conflicting mapping metadata")
			require.Empty(t, out)
		})
	}
}

// The model's one list of visibilities is what every path judges against.
func TestKnownVisibilityIsTheOneList(t *testing.T) {
	for _, known := range []string{"", resources.VisibilityPrivate, resources.VisibilityInternal, resources.VisibilityPublic} {
		require.True(t, resources.KnownVisibility(known), known)
	}
	require.False(t, resources.KnownVisibility("pubilc"))
	require.False(t, resources.KnownVisibility("everyone"))
	require.False(t, resources.KnownVisibility("module"), "every module is said with internal and a wildcard allow-list")
	require.False(t, resources.KnownVisibility("external"), "where an endpoint lives is its location")
	require.True(t, resources.KnownLocation(""))
	require.True(t, resources.KnownLocation(resources.LocationExternal))
	require.False(t, resources.KnownLocation("nowhere"))
}

// Removing the well-formed references from a value must not let the text
// before one and the text after it spell a marker the value never carried: the
// spans between references are judged where they are.
func TestTextAroundAWellFormedReferenceIsNotAMarker(t *testing.T) {
	ctx := context.Background()
	selection := resources.EndpointSelectionContext{ConsumerModule: "payments",
		Declared: declaredBy(selectionEndpoint("rpc", "grpc", "public"))}
	mappings := []*basev0.NetworkMapping{selectionMapping("rpc", "grpc", nativeAt("http://localhost:9090"))}
	for name, value := range map[string]string{
		"a dollar before and a brace after": "$${endpoint:" + selectionUnique + "/rpc}{endpoint:tail",
		"prefix split by two references":    "${endpoint:" + selectionUnique + "/rpc}${endpoint:" + selectionUnique + "/rpc}",
		"the prefix letters around one":     "${endpoint${endpoint:" + selectionUnique + "/rpc}:x",
	} {
		t.Run(name, func(t *testing.T) {
			out, err := resources.InterpolateEndpointsFor(ctx, value, mappings, resources.NewNativeNetworkAccess(), selection)
			require.NoError(t, err)
			require.NotContains(t, out, "${endpoint:"+selectionUnique)
			require.Contains(t, out, "localhost:9090")
		})
	}
	for name, value := range map[string]string{
		"a real marker after a reference":  "${endpoint:" + selectionUnique + "/rpc},${endpoint:",
		"a real marker before a reference": "${endpoint:x,${endpoint:" + selectionUnique + "/rpc}",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resources.InterpolateEndpointsFor(ctx, value, mappings, resources.NewNativeNetworkAccess(), selection)
			require.ErrorIs(t, err, resources.ErrMalformedEndpointReference)
		})
	}
}

// A declaration the model cannot judge is refused by every wiring path, for
// the owning module exactly as for a foreign one: it is a fault of the
// producer's manifest, not a verdict on the consumer, and a "consumes all"
// dependency never drops it as though it had been denied.
func TestAnInvalidDeclarationIsRefusedByEveryWiringPath(t *testing.T) {
	endpoints := []*basev0.Endpoint{
		{Module: "platform", Service: "api", Name: "open", Api: "grpc", Visibility: resources.VisibilityPublic},
		{Module: "platform", Service: "api", Name: "broken", Api: "http", Visibility: "pubilc"},
	}
	mappings := []*basev0.NetworkMapping{
		{Endpoint: endpoints[0], Instances: []*basev0.NetworkInstance{nativeAt("http://localhost:9090")}},
		{Endpoint: endpoints[1], Instances: []*basev0.NetworkInstance{nativeAt("http://localhost:8080")}},
	}
	dependency := &resources.ServiceDependency{Name: "api", Module: "platform"}
	for _, consumer := range []string{"platform", "payments"} {
		t.Run(consumer, func(t *testing.T) {
			_, err := resources.ConsumedDependencyEndpoints(consumer, dependency, endpoints)
			require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration, "consumed")
			_, err = resources.PermittedDependencyEndpoints(consumer, dependency, endpoints)
			require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration, "permitted")
			_, err = resources.ResolveDependencyNetworkMappings(consumer, []*resources.ServiceDependency{dependency}, mappings)
			require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration, "network mappings")
		})
	}
}
