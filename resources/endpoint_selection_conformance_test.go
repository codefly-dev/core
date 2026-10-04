package resources_test

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// The conformance cases for per-reference canonical endpoint selection.
//
// Each one is a composition that resolved wrongly, or could only be made to
// resolve correctly by a consumer of this package modelling this scan beside it.
// They are here, against core's own resolution, because that is the only place
// the answer can be given once: a caller can order and prune the mappings it
// hands over, but it cannot stop this function matching by API, and every rule
// it writes to compensate is a second model of a scan it does not own.

const (
	selectionModule  = "platform"
	selectionService = "authority"
	selectionUnique  = selectionModule + "/" + selectionService
)

func selectionEndpoint(name, api, visibility string) *resources.Endpoint {
	return &resources.Endpoint{
		Module: selectionModule, Service: selectionService,
		Name: name, API: api, Visibility: visibility,
	}
}

func selectionMapping(name, api string, instances ...*basev0.NetworkInstance) *basev0.NetworkMapping {
	return &basev0.NetworkMapping{
		Endpoint:  &basev0.Endpoint{Module: selectionModule, Service: selectionService, Name: name, Api: api},
		Instances: instances,
	}
}

func nativeAt(address string) *basev0.NetworkInstance {
	return &basev0.NetworkInstance{Address: address, Access: &basev0.NetworkAccess{Kind: resources.NetworkAccessNative}}
}

func containerAt(address string) *basev0.NetworkInstance {
	return &basev0.NetworkInstance{Address: address, Access: &basev0.NetworkAccess{Kind: resources.NetworkAccessContainer}}
}

// declaredBy answers a selection context's manifest lookup for one producer.
func declaredBy(endpoints ...*resources.Endpoint) func(string) []*resources.Endpoint {
	return func(unique string) []*resources.Endpoint {
		if unique != selectionUnique {
			return nil
		}
		return endpoints
	}
}

func resolveFor(t *testing.T, reference string, mappings []*basev0.NetworkMapping,
	access *basev0.NetworkAccess, selection resources.EndpointSelectionContext,
) (string, error) {
	t.Helper()
	return resources.InterpolateEndpointsFor(context.Background(), "${endpoint:"+reference+"}", mappings, access, selection)
}

// A reference names ONE endpoint, and publication order is not part of the
// answer.
//
// `grpc`, `admin` and `metrics` all carry api `grpc`, so all three satisfy
// ${…/grpc} under the matcher. The scan used to take the first that had an
// instance for the access, so which address a value got depended on the order
// the mappings were recorded in — and a consumer of this package could only fix
// that by sorting the mappings before handing them over, which is a model of
// this scan rather than an answer from it.
func TestAReferenceResolvesToTheEndpointItNamesInAnyOrder(t *testing.T) {
	grpc := selectionMapping("grpc", "grpc", nativeAt("http://localhost:1111"))
	admin := selectionMapping("admin", "grpc", nativeAt("http://localhost:2222"))
	metrics := selectionMapping("metrics", "grpc", nativeAt("http://localhost:3333"))

	for _, order := range []struct {
		name     string
		mappings []*basev0.NetworkMapping
	}{
		{"named first", []*basev0.NetworkMapping{grpc, admin, metrics}},
		{"named last", []*basev0.NetworkMapping{admin, metrics, grpc}},
		{"named between", []*basev0.NetworkMapping{admin, grpc, metrics}},
	} {
		t.Run(order.name, func(t *testing.T) {
			out, err := resolveFor(t, selectionUnique+"/grpc", order.mappings, resources.NewNativeNetworkAccess(),
				resources.EndpointSelectionContext{})
			require.NoError(t, err)
			require.Equal(t, "http://localhost:1111", out)

			// And each sibling still answers the reference that names it.
			out, err = resolveFor(t, selectionUnique+"/metrics", order.mappings, resources.NewNativeNetworkAccess(),
				resources.EndpointSelectionContext{})
			require.NoError(t, err)
			require.Equal(t, "http://localhost:3333", out)
		})
	}
}

// An endpoint that published no mapping is reported as absent, never replaced by
// a sibling that shares its API.
func TestAnAbsentExactNameIsNotAnsweredByAnAPISibling(t *testing.T) {
	mappings := []*basev0.NetworkMapping{
		selectionMapping("admin", "grpc", nativeAt("http://localhost:2222")),
	}
	declared := resources.EndpointSelectionContext{
		ConsumerModule: "payments",
		Declared: declaredBy(
			selectionEndpoint("grpc", "grpc", "public"),
			selectionEndpoint("admin", "grpc", "public"),
		),
	}

	out, err := resolveFor(t, selectionUnique+"/grpc", mappings, resources.NewNativeNetworkAccess(), declared)
	require.Error(t, err, "the endpoint the reference names published nothing")
	require.Contains(t, err.Error(), `"grpc"`)
	require.NotContains(t, err.Error(), "localhost:2222")
	require.Empty(t, out)

	// And the reason the manifest is not optional for this case. With only the
	// mappings to go on, `${…/grpc}` naming an endpoint that published nothing
	// is indistinguishable from `${…/grpc}` meaning "the endpoint whose API is
	// grpc" — which is the matcher's documented behaviour and what a
	// composition referring to an API by name relies on. So the sibling DOES
	// answer here, and it is not a defect: nothing in this input says `grpc` is
	// an endpoint at all.
	//
	// It is why WithConsumer exists and why a caller that can name its
	// producer's manifest must pass it. The manifest is the only input that
	// distinguishes "the endpoint you named is not running" from "you named an
	// API".
	out, err = resolveFor(t, selectionUnique+"/grpc", mappings, resources.NewNativeNetworkAccess(),
		resources.EndpointSelectionContext{})
	require.NoError(t, err)
	require.Equal(t, "http://localhost:2222", out,
		"with no manifest the token is an API, and the one endpoint carrying it answers")
}

// The named endpoint having no address for this access is reported as that, and
// the scan does not continue into a sibling that does — including when the
// sibling's own address is empty, which used to be a separate hazard for a
// caller trying to predict this.
func TestAnEndpointWithNoAddressForThisAccessIsNotReplaced(t *testing.T) {
	for _, sibling := range []struct {
		name     string
		instance *basev0.NetworkInstance
	}{
		{"the sibling has an address", nativeAt("http://localhost:2222")},
		{"the sibling's address is empty", nativeAt("")},
	} {
		t.Run(sibling.name, func(t *testing.T) {
			mappings := []*basev0.NetworkMapping{
				// The named endpoint is reachable, but not from here.
				selectionMapping("grpc", "grpc", containerAt("http://grpc:9090")),
				selectionMapping("admin", "grpc", sibling.instance),
			}
			out, err := resolveFor(t, selectionUnique+"/grpc", mappings, resources.NewNativeNetworkAccess(),
				resources.EndpointSelectionContext{})
			require.Error(t, err)
			require.Contains(t, err.Error(), "no instance for access")
			require.Contains(t, err.Error(), `"grpc"`)
			require.NotContains(t, err.Error(), "localhost:2222")
			require.Empty(t, out)
		})
	}
}

// A producer publishing several mappings for one endpoint is several places to
// look, not a contradiction: the first with an instance for this access answers,
// whichever that is.
func TestSeveralMappingsForOneEndpointAreSearchedInOrder(t *testing.T) {
	for _, order := range []struct {
		name     string
		mappings []*basev0.NetworkMapping
	}{
		{"the access is on the first", []*basev0.NetworkMapping{
			selectionMapping("grpc", "grpc", nativeAt("http://localhost:1111")),
			selectionMapping("grpc", "grpc", containerAt("http://grpc:9090")),
		}},
		{"the access is on the second", []*basev0.NetworkMapping{
			selectionMapping("grpc", "grpc", containerAt("http://grpc:9090")),
			selectionMapping("grpc", "grpc", nativeAt("http://localhost:1111")),
		}},
	} {
		t.Run(order.name, func(t *testing.T) {
			// A sibling is present throughout, so a fall-through would be
			// visible rather than merely absent.
			mappings := append(order.mappings, selectionMapping("admin", "grpc", nativeAt("http://localhost:2222")))
			out, err := resolveFor(t, selectionUnique+"/grpc", mappings, resources.NewNativeNetworkAccess(),
				resources.EndpointSelectionContext{})
			require.NoError(t, err)
			require.Equal(t, "http://localhost:1111", out)
		})
	}
}

// An exact name the consumer may NOT reach is refused with the visibility
// reason. It is never resolved to a permitted sibling that shares its API.
//
// This is the case a caller of this package could not fix at all. The reference
// names `grpc`, which is private to the producer's module; `admin` is public and
// shares its API. Answering with `admin` hands a cross-module consumer an
// address for an endpoint it asked for and was refused — under a different
// endpoint's identity, with no error. The honest answer is the refusal, and the
// composition names an endpoint it may reach or the producer exports the one it
// means.
func TestAnExactNameTheConsumerMayNotReachIsRefusedNotSubstituted(t *testing.T) {
	mappings := []*basev0.NetworkMapping{
		selectionMapping("grpc", "grpc", nativeAt("http://localhost:1111")),
		selectionMapping("admin", "grpc", nativeAt("http://localhost:2222")),
	}
	selection := resources.EndpointSelectionContext{
		ConsumerModule: "payments",
		Declared: declaredBy(
			selectionEndpoint("grpc", "grpc", "private"),
			selectionEndpoint("admin", "grpc", "public"),
		),
	}

	out, err := resolveFor(t, selectionUnique+"/grpc", mappings, resources.NewNativeNetworkAccess(), selection)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private to module")
	require.NotContains(t, err.Error(), "localhost:2222", "the permitted sibling must not stand in for a refused endpoint")
	require.Empty(t, out)

	// The producer's own module reaches it, and gets its address.
	own := selection
	own.ConsumerModule = selectionModule
	out, err = resolveFor(t, selectionUnique+"/grpc", mappings, resources.NewNativeNetworkAccess(), own)
	require.NoError(t, err)
	require.Equal(t, "http://localhost:1111", out)
}

// With no exact name, the reference must come to exactly one endpoint the
// consumer may reach — and more than one is refused rather than decided by
// declaration order.
func TestAnAPIReferenceSeveralEndpointsSatisfyIsRefused(t *testing.T) {
	mappings := []*basev0.NetworkMapping{
		selectionMapping("primary", "grpc", nativeAt("http://localhost:1111")),
		selectionMapping("secondary", "grpc", nativeAt("http://localhost:2222")),
	}
	declared := declaredBy(
		selectionEndpoint("primary", "grpc", "public"),
		selectionEndpoint("secondary", "grpc", "public"),
	)

	_, err := resolveFor(t, selectionUnique+"/grpc", mappings, resources.NewNativeNetworkAccess(),
		resources.EndpointSelectionContext{ConsumerModule: "payments", Declared: declared})
	require.Error(t, err)
	require.ErrorIs(t, err, resources.ErrAmbiguousEndpointReference)
	require.Contains(t, err.Error(), "primary")
	require.Contains(t, err.Error(), "secondary")

	// One of them private to the producer narrows it to one for a cross-module
	// consumer, which is unambiguous and resolves.
	narrowed := declaredBy(
		selectionEndpoint("primary", "grpc", "public"),
		selectionEndpoint("secondary", "grpc", "private"),
	)
	out, err := resolveFor(t, selectionUnique+"/grpc", mappings, resources.NewNativeNetworkAccess(),
		resources.EndpointSelectionContext{ConsumerModule: "payments", Declared: narrowed})
	require.NoError(t, err)
	require.Equal(t, "http://localhost:1111", out)
}

// Selection is scoped to the producer the reference names. An endpoint of
// another service that happens to share the token is not a candidate.
func TestSelectionIsScopedToTheProducerTheReferenceNames(t *testing.T) {
	selected, err := resources.SelectEndpointForReference("payments",
		&resources.EndpointInformation{Module: selectionModule, Service: selectionService, Name: "grpc"},
		[]*resources.Endpoint{
			{Module: "payments", Service: "ledger", Name: "grpc", API: "grpc", Visibility: "public"},
			selectionEndpoint("grpc", "grpc", "public"),
		})
	require.NoError(t, err)
	require.Equal(t, selectionModule, selected.Endpoint.Module)
	require.True(t, selected.ExactName)

	_, err = resources.SelectEndpointForReference("payments",
		&resources.EndpointInformation{Module: selectionModule, Service: selectionService, Name: "grpc"},
		[]*resources.Endpoint{
			{Module: "payments", Service: "ledger", Name: "grpc", API: "grpc", Visibility: "public"},
		})
	require.Error(t, err, "another producer's endpoint cannot answer this reference")
	require.ErrorIs(t, err, resources.ErrNoSuchEndpoint)
}

// An unidentified consumer is not judged on visibility: a caller that has not
// said which module receives the address has given no evidence to refuse on.
func TestAnUnidentifiedConsumerIsNotJudgedOnVisibility(t *testing.T) {
	selected, err := resources.SelectEndpointForReference("",
		&resources.EndpointInformation{Module: selectionModule, Service: selectionService, Name: "grpc"},
		[]*resources.Endpoint{selectionEndpoint("grpc", "grpc", "private")})
	require.NoError(t, err)
	require.Equal(t, "grpc", selected.Endpoint.Name)
}

// The plan-time check and the resolution reach the same verdict, because they
// ask the same function. A check that passed a composition the resolution then
// answered differently is the fault this shared body exists to remove.
func TestTheCheckAndTheResolutionSelectTheSameEndpoint(t *testing.T) {
	declared := []*resources.Endpoint{
		// The private exact name FIRST, which is the order that used to decide
		// the check's verdict.
		selectionEndpoint("grpc", "grpc", "private"),
		selectionEndpoint("admin", "grpc", "public"),
	}
	_, err := resources.SelectEndpointForReference("payments",
		&resources.EndpointInformation{Module: selectionModule, Service: selectionService, Name: "grpc"}, declared)
	require.Error(t, err, "the check refuses an exact name the consumer may not reach")

	mappings := []*basev0.NetworkMapping{
		selectionMapping("grpc", "grpc", nativeAt("http://localhost:1111")),
		selectionMapping("admin", "grpc", nativeAt("http://localhost:2222")),
	}
	_, resolveErr := resolveFor(t, selectionUnique+"/grpc", mappings, resources.NewNativeNetworkAccess(),
		resources.EndpointSelectionContext{ConsumerModule: "payments", Declared: declaredBy(declared...)})
	require.Error(t, resolveErr, "and so does the resolution, for the same reason")
	// The same reason, not merely the same verdict: the resolution reports the
	// selection's own refusal, so the two cannot drift into agreeing by
	// coincidence.
	require.Contains(t, resolveErr.Error(), err.Error())
	require.Contains(t, resolveErr.Error(), "private to module")
}
