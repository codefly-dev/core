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
	err := resources.ValidateEndpointVisibility(selectionModule, selectionModule, selectionService, "grpc", "pubilc", "", nil)
	require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration)
	require.NoError(t, resources.ValidateEndpointVisibility(selectionModule, selectionModule, selectionService, "grpc", "private", "", nil))

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

// A diagnostic never carries the configuration value: a value may be a
// secret, and an error travels further than the value was meant to. The
// references it names and the key are what a reader needs.
func TestReferenceDiagnosticsDoNotExposeTheValue(t *testing.T) {
	ctx := context.Background()
	const secret = "syntheticsecret7f3a"
	inRun := resources.WithRunProducers(func(string) bool { return true })
	consumer := resources.WithConsumer("payments", declaredBy(selectionEndpoint("grpc", "grpc", "public")))
	for name, value := range map[string]string{
		"malformed marker":                  "credential=" + secret + ";${endpoint:}",
		"beside a valid reference":          "credential=" + secret + ";${endpoint:" + selectionUnique + "/grpc};${endpoint:",
		"unknown producer beside a secret":  "credential=" + secret + ";${endpoint:nobody/nothing/grpc}",
		"the secret inside a marker body":   "${endpoint:};${endpoint:credential=" + secret + "}",
		"a marker body that is the secret":  "${endpoint:credential=" + secret + "}",
		"an unterminated marker eating it":  "${endpoint:platform/x/credential=" + secret + "}",
		"a bad api qualifier carrying it":   "${endpoint:" + selectionUnique + "/grpc::" + secret + "}",
		"a secret that is a valid name":     "${endpoint:" + selectionUnique + "/" + secret + "}",
		"a secret that is a valid producer": "${endpoint:" + secret + "/chat/grpc}",
		"a secret that is a valid module":   "${endpoint:" + secret + "/" + secret + "/grpc}",
		"an empty api qualifier":            "credential=" + secret + ";${endpoint:" + selectionUnique + "/grpc::}",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resources.InterpolateEndpointsFor(ctx, value, nil, resources.NewNativeNetworkAccess(), resources.EndpointSelectionContext{ConsumerModule: "payments", Declared: declaredBy(selectionEndpoint("grpc", "grpc", "public"))})
			require.Error(t, err)
			require.NotContains(t, err.Error(), secret)
			_, err = resources.InterpolateEndpointsFor(ctx, value, nil, resources.NewNativeNetworkAccess(), resources.EndpointSelectionContext{})
			require.Error(t, err, "an incomplete selection refuses")
			require.NotContains(t, err.Error(), secret, "and names no value doing so")
			for _, conf := range []*basev0.Configuration{authorityConf("credential", value), authorityTemplated("credential", value)} {
				conf.Infos[0].ConfigurationValues[0].Secret = true
				_, err = resources.InterpolateRunWideConfigurationEndpoints(ctx, conf, nil, resources.NewNativeNetworkAccess(), inRun, consumer)
				require.Error(t, err)
				require.NotContains(t, err.Error(), secret)
				require.Contains(t, err.Error(), "authority/credential", "the key is named")
				_, err = resources.InterpolateConfigurationEndpoints(ctx, conf, nil, resources.NewNativeNetworkAccess(), inRun, consumer)
				require.Error(t, err)
				require.NotContains(t, err.Error(), secret)
			}
		})
	}
}

// A module-less declaration is scoped under the module the reference named,
// and a refusal of it must not print that module: it is text from the value.
// Positions are numbered across the whole value, the same number the plan-time
// check reports, so a render and the check name one reference the same way.
func TestARefusalNeverPrintsTheModuleTheReferenceNamed(t *testing.T) {
	ctx := context.Background()
	const secret = "syntheticsecret3b7c"
	declared := func(unique string) ([]*resources.Endpoint, bool) {
		return []*resources.Endpoint{{Name: "grpc", API: "grpc", Visibility: resources.VisibilityPrivate}}, unique == secret+"/authority"
	}
	value := "${endpoint:" + secret + "/authority/grpc}"
	_, err := resources.InterpolateEndpointsFor(ctx, value, nil, resources.NewNativeNetworkAccess(), resources.EndpointSelectionContext{ConsumerModule: "payments", Declared: declared})
	require.ErrorIs(t, err, resources.ErrEndpointNotReachable)
	require.NotContains(t, err.Error(), secret)
	require.Contains(t, err.Error(), "reference 1 of 1")
	conf := authorityConf("credential", value)
	conf.Infos[0].ConfigurationValues[0].Secret = true
	_, err = resources.InterpolateConfigurationEndpoints(ctx, conf, nil, resources.NewNativeNetworkAccess(),
		resources.WithRunProducers(func(string) bool { return true }), resources.WithConsumer("payments", declared))
	require.Error(t, err)
	require.NotContains(t, err.Error(), secret)

	// Numbering across the value and its literals: the failing reference in
	// the second literal is "reference 3 of 3" — after the value's one and the
	// first literal's one — however the parts are rendered.
	public := declaredBy(selectionEndpoint("rpc", "grpc", "public"), selectionEndpoint("hidden", "grpc", "private"))
	mappings := []*basev0.NetworkMapping{selectionMapping("rpc", "grpc", nativeAt("http://localhost:9090"))}
	templated := authorityTemplated("address", "a=${endpoint:"+selectionUnique+"/rpc}", "b=${endpoint:"+selectionUnique+"/hidden}")
	templated.Infos[0].ConfigurationValues[0].Value = "${endpoint:" + selectionUnique + "/rpc}"
	_, err = resources.InterpolateConfigurationEndpoints(ctx, templated, mappings, resources.NewNativeNetworkAccess(),
		resources.WithRunProducers(func(string) bool { return true }), resources.WithConsumer("payments", public))
	require.ErrorIs(t, err, resources.ErrEndpointNotReachable)
	require.Contains(t, err.Error(), "reference 3 of 3")
}

// Where an endpoint lives is its Location and nothing else: a visibility that
// spells "external" is an invalid declaration, never a location.
func TestLocationIsTheOnlyRecordOfWhereAnEndpointLives(t *testing.T) {
	spelled := &resources.Endpoint{Module: "infra", Service: "db", Name: "tcp", API: "tcp", Visibility: "external"}
	require.False(t, spelled.External())
	require.False(t, resources.IsExternalEndpoint(&basev0.Endpoint{Module: "infra", Service: "db", Name: "tcp", Api: "tcp", Visibility: "external"}))
	located := &resources.Endpoint{Module: "infra", Service: "db", Name: "tcp", API: "tcp", Visibility: resources.VisibilityPrivate, Location: resources.LocationExternal}
	require.True(t, located.External())
	require.True(t, resources.IsExternalEndpoint(&basev0.Endpoint{Module: "infra", Service: "db", Name: "tcp", Api: "tcp", Location: resources.LocationExternal}))
}

// The whole declaration is judged at every typed boundary, not only when YAML
// is read: an allow-list on a visibility that never reads one, or a location
// the model does not define, is an invalid declaration for the selection, for
// dependency wiring, for the proto conversion and for the schema — and never a
// per-consumer denial a run-wide read may drop.
func TestTheWholeDeclarationIsJudgedAtEveryTypedBoundary(t *testing.T) {
	ctx := context.Background()
	for name, broken := range map[string]*resources.Endpoint{
		"an allow-list nothing reads (public)":  {Module: "platform", Service: "api", Name: "open", API: "grpc", Visibility: resources.VisibilityPublic, AllowModules: []string{"payments"}},
		"an allow-list nothing reads (private)": {Module: "platform", Service: "api", Name: "open", API: "grpc", Visibility: resources.VisibilityPrivate, AllowModules: []string{"payments"}},
		"an unknown location":                   {Module: "platform", Service: "api", Name: "open", API: "grpc", Visibility: resources.VisibilityPublic, Location: "nowhere"},
	} {
		t.Run(name, func(t *testing.T) {
			info := &resources.EndpointInformation{Module: "platform", Service: "api", Name: "open"}
			for _, consumer := range []string{"platform", "payments", "other"} {
				_, err := resources.SelectEndpointForReference(consumer, info, []*resources.Endpoint{broken})
				require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration, "selection for %s", consumer)
				require.NotErrorIs(t, err, resources.ErrEndpointNotReachable, "never a denial")
			}
			_, err := broken.Proto()
			require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration, "proto conversion")

			typed := &basev0.Endpoint{Module: broken.Module, Service: broken.Service, Name: broken.Name, Api: broken.API, Visibility: broken.Visibility, Location: broken.Location, AllowModules: broken.AllowModules}
			dependency := &resources.ServiceDependency{Name: "api", Module: "platform"}
			for _, consumer := range []string{"platform", "payments"} {
				_, err := resources.ConsumedDependencyEndpoints(consumer, dependency, []*basev0.Endpoint{typed})
				require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration, "wiring for %s", consumer)
			}
			// Run-wide, the fault is refused with the key named, never dropped.
			conf := authorityConf("address", "${endpoint:platform/api/open}")
			consumerCtx := resources.WithConsumer("payments", func(unique string) ([]*resources.Endpoint, bool) {
				return []*resources.Endpoint{broken}, unique == "platform/api"
			})
			_, err = resources.InterpolateRunWideConfigurationEndpoints(ctx, conf, nil, resources.NewNativeNetworkAccess(),
				resources.WithRunProducers(func(string) bool { return true }), consumerCtx)
			require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration)
			require.Contains(t, err.Error(), "authority/address")
		})
	}
	// The schema carries the allow-list rule itself, so a typed endpoint that
	// never went through this package's conversion is refused on the wire too.
	typed := &basev0.Endpoint{Module: "platform", Service: "api", Name: "open", Api: "grpc", Visibility: resources.VisibilityPublic, AllowModules: []string{"payments"}}
	require.Error(t, resources.Validate(typed), "the schema refuses an allow-list on a public endpoint")
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
