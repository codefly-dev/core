package resources_test

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// A composition fault anywhere in a value dominates an omission the run-wide
// path would be entitled to make on another reference of the same value —
// whichever came first, and whether the fault sits in the value or in a template
// literal. Deciding on the first failure let the order of two references decide
// whether a fault was reported or the key silently dropped.
func TestAFaultBehindAnOmittableReferenceIsStillRefused(t *testing.T) {
	ctx := context.Background()
	inRun := resources.WithRunProducers(func(unique string) bool { return unique == selectionUnique })
	// `hidden` is private (an omission for payments); `grpc` as an API token
	// matches two public siblings (ambiguous: a fault).
	consumer := resources.WithConsumer("payments", declaredBy(
		selectionEndpoint("hidden", "grpc", "private"),
		selectionEndpoint("primary", "grpc", "public"),
		selectionEndpoint("secondary", "grpc", "public"),
	))
	mappings := []*basev0.NetworkMapping{
		selectionMapping("hidden", "grpc", nativeAt("http://localhost:1")),
		selectionMapping("primary", "grpc", nativeAt("http://localhost:2")),
		selectionMapping("secondary", "grpc", nativeAt("http://localhost:3")),
	}
	omittable := "${endpoint:" + selectionUnique + "/hidden}"
	ambiguous := "${endpoint:" + selectionUnique + "/grpc}"
	malformed := "${endpoint:nonsense}"

	plain := func(value string) *basev0.Configuration {
		return &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{{
			Name: "authority", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "address", Value: value}},
		}}}
	}
	templated := func(literals ...string) *basev0.Configuration {
		var segments []*basev0.ConfigurationValueTemplateSegment
		for _, literal := range literals {
			segments = append(segments, &basev0.ConfigurationValueTemplateSegment{Content: &basev0.ConfigurationValueTemplateSegment_Literal{Literal: literal}})
		}
		return &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{{
			Name: "authority", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "address", Template: &basev0.ConfigurationValueTemplate{Segments: segments}}},
		}}}
	}

	for name, tc := range map[string]struct {
		conf *basev0.Configuration
		want error
	}{
		"omission then ambiguity":             {plain(omittable + "," + ambiguous), resources.ErrAmbiguousEndpointReference},
		"ambiguity then omission":             {plain(ambiguous + "," + omittable), resources.ErrAmbiguousEndpointReference},
		"omission then malformed":             {plain(omittable + "," + malformed), nil},
		"malformed then omission":             {plain(malformed + "," + omittable), nil},
		"omission literal, ambiguity literal": {templated(omittable, ambiguous), resources.ErrAmbiguousEndpointReference},
		"ambiguity literal, omission literal": {templated(ambiguous, omittable), resources.ErrAmbiguousEndpointReference},
		"omission literal, malformed literal": {templated(omittable, malformed), nil},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resources.InterpolateRunWideConfigurationEndpoints(ctx, tc.conf, mappings, resources.NewNativeNetworkAccess(), inRun, consumer)
			require.Error(t, err, "the fault is refused, not hidden behind the omission")
			require.Contains(t, err.Error(), "authority/address", "the refusal names the configuration and key")
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			}
			// And the strict path refuses the same value for the same reason.
			_, err = resources.InterpolateConfigurationEndpoints(ctx, tc.conf, mappings, resources.NewNativeNetworkAccess(), inRun, consumer)
			require.Error(t, err)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			}
		})
	}

	// The control: a value whose only failure IS the omission is omitted.
	resolved, err := resources.InterpolateRunWideConfigurationEndpoints(ctx, plain(omittable), mappings, resources.NewNativeNetworkAccess(), inRun, consumer)
	require.NoError(t, err)
	require.Empty(t, resolved.GetInfos(), "the one value was not for this consumer, so the information is omitted")
}

// An invalid declaration — an unsupported visibility value — is not a consumer
// being denied by a valid export policy. It is refused with the configuration
// and key, plain or templated, even when it is the value's only reference.
func TestAnUnsupportedVisibilityIsAFaultNotAnOmission(t *testing.T) {
	ctx := context.Background()
	inRun := resources.WithRunProducers(func(unique string) bool { return unique == selectionUnique })
	consumer := resources.WithConsumer("payments", declaredBy(selectionEndpoint("grpc", "grpc", "pubilc")))
	mappings := []*basev0.NetworkMapping{selectionMapping("grpc", "grpc", nativeAt("http://localhost:1111"))}
	reference := "${endpoint:" + selectionUnique + "/grpc}"

	plain := &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{{
		Name: "authority", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "address", Value: reference}},
	}}}
	templated := &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{{
		Name: "authority", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "address", Template: &basev0.ConfigurationValueTemplate{
			Segments: []*basev0.ConfigurationValueTemplateSegment{{Content: &basev0.ConfigurationValueTemplateSegment_Literal{Literal: "grpc://" + reference}}},
		}}},
	}}}
	for name, conf := range map[string]*basev0.Configuration{"plain": plain, "templated": templated} {
		t.Run(name, func(t *testing.T) {
			_, err := resources.InterpolateRunWideConfigurationEndpoints(ctx, conf, mappings, resources.NewNativeNetworkAccess(), inRun, consumer)
			require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration)
			require.NotErrorIs(t, err, resources.ErrEndpointNotReachable)
			require.Contains(t, err.Error(), "authority/address")
			require.Contains(t, err.Error(), `"pubilc"`)
		})
	}

	// Alongside it, the valid private endpoint is still an omission.
	private := resources.WithConsumer("payments", declaredBy(selectionEndpoint("grpc", "grpc", "private")))
	resolved, err := resources.InterpolateRunWideConfigurationEndpoints(ctx, plain, mappings, resources.NewNativeNetworkAccess(), inRun, private)
	require.NoError(t, err)
	require.Empty(t, resolved.GetInfos())
}
