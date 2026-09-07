package resources_test

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gatewayMappings() []*basev0.NetworkMapping {
	return []*basev0.NetworkMapping{
		{
			Endpoint: &basev0.Endpoint{Module: "saas-starter", Service: "auth-sidecar", Name: "http", Api: standards.HTTP},
			Instances: []*basev0.NetworkInstance{
				{Address: "http://localhost:1234", Access: &basev0.NetworkAccess{Kind: resources.NetworkAccessNative}},
				{Address: "http://host.docker.internal:1234", Access: &basev0.NetworkAccess{Kind: resources.NetworkAccessContainer}},
			},
		},
	}
}

func TestInterpolateEndpoints(t *testing.T) {
	ctx := context.Background()
	mappings := gatewayMappings()

	t.Run("resolves a reference embedded in a URL", func(t *testing.T) {
		out, err := resources.InterpolateEndpoints(ctx,
			"${endpoint:saas-starter/auth-sidecar/http}/v1/auth/.well-known/jwks.json",
			mappings, resources.NewNativeNetworkAccess())
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:1234/v1/auth/.well-known/jwks.json", out)
	})

	t.Run("resolves for the requested access", func(t *testing.T) {
		out, err := resources.InterpolateEndpoints(ctx,
			"${endpoint:saas-starter/auth-sidecar/http}",
			mappings, resources.NewContainerNetworkAccess())
		require.NoError(t, err)
		assert.Equal(t, "http://host.docker.internal:1234", out)
	})

	t.Run("leaves a value without a reference unchanged", func(t *testing.T) {
		out, err := resources.InterpolateEndpoints(ctx, "https://static.example.com", mappings, resources.NewNativeNetworkAccess())
		require.NoError(t, err)
		assert.Equal(t, "https://static.example.com", out)
	})

	t.Run("errors on an unknown endpoint", func(t *testing.T) {
		_, err := resources.InterpolateEndpoints(ctx,
			"${endpoint:saas-starter/auth-sidecar/grpc}",
			mappings, resources.NewNativeNetworkAccess())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("errors when the endpoint has no instance for the access", func(t *testing.T) {
		_, err := resources.InterpolateEndpoints(ctx,
			"${endpoint:saas-starter/auth-sidecar/http}",
			mappings, resources.NewPublicNetworkAccess())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no instance for access=public")
	})

	t.Run("errors on an empty resolved address instead of emitting a broken URL", func(t *testing.T) {
		blank := []*basev0.NetworkMapping{
			{
				Endpoint:  &basev0.Endpoint{Module: "saas-starter", Service: "auth-sidecar", Name: "http", Api: standards.HTTP},
				Instances: []*basev0.NetworkInstance{{Address: "", Access: resources.NewNativeNetworkAccess()}},
			},
		}
		_, err := resources.InterpolateEndpoints(ctx,
			"${endpoint:saas-starter/auth-sidecar/http}/v1/jwks",
			blank, resources.NewNativeNetworkAccess())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty address")
	})

	t.Run("errors on a reference that names no endpoint", func(t *testing.T) {
		_, err := resources.InterpolateEndpoints(ctx,
			"${endpoint:saas-starter/auth-sidecar}",
			mappings, resources.NewNativeNetworkAccess())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must name an endpoint")
	})
}

func TestInterpolateConfigurationEndpoints(t *testing.T) {
	ctx := context.Background()
	conf := &basev0.Configuration{
		Origin: resources.ConfigurationWorkspace,
		Infos: []*basev0.ConfigurationInformation{
			{
				Name: "work-context",
				ConfigurationValues: []*basev0.ConfigurationValue{
					{Key: "authority-jwks-url", Value: "${endpoint:saas-starter/auth-sidecar/http}/v1/auth/.well-known/jwks.json"},
					{Key: "static", Value: "keep-me"},
				},
			},
		},
	}
	resolved, err := resources.InterpolateConfigurationEndpoints(ctx, conf, gatewayMappings(), resources.NewNativeNetworkAccess())
	require.NoError(t, err)

	url, err := resources.GetConfigurationValue(ctx, resolved, "work-context", "authority-jwks-url")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:1234/v1/auth/.well-known/jwks.json", url)

	static, err := resources.GetConfigurationValue(ctx, resolved, "work-context", "static")
	require.NoError(t, err)
	assert.Equal(t, "keep-me", static)

	// The consumer's access is not baked into the shared configuration: the input
	// keeps its reference, so it can be resolved again for a different access.
	raw, err := resources.GetConfigurationValue(ctx, conf, "work-context", "authority-jwks-url")
	require.NoError(t, err)
	assert.Equal(t, "${endpoint:saas-starter/auth-sidecar/http}/v1/auth/.well-known/jwks.json", raw)

	container, err := resources.InterpolateConfigurationEndpoints(ctx, conf, gatewayMappings(), resources.NewContainerNetworkAccess())
	require.NoError(t, err)
	url, err = resources.GetConfigurationValue(ctx, container, "work-context", "authority-jwks-url")
	require.NoError(t, err)
	assert.Equal(t, "http://host.docker.internal:1234/v1/auth/.well-known/jwks.json", url)
}

// The strict variant is fail-fast: any reference that does not resolve for the
// consumer is a hard error, whether the service is absent from the mapping set or
// present under a different endpoint. This is what preserves typo detection on the
// GetWorkspaceConfigurations path.
func TestInterpolateConfigurationEndpointsErrorsOnUnresolvedReference(t *testing.T) {
	ctx := context.Background()

	// Service present, wrong endpoint token.
	wrongEndpoint := &basev0.Configuration{
		Origin: resources.ConfigurationWorkspace,
		Infos: []*basev0.ConfigurationInformation{
			{
				Name: "work-context",
				ConfigurationValues: []*basev0.ConfigurationValue{
					{Key: "authority-jwks-url", Value: "${endpoint:saas-starter/auth-sidecar/grpc}/v1/jwks"},
				},
			},
		},
	}
	_, err := resources.InterpolateConfigurationEndpoints(ctx, wrongEndpoint, gatewayMappings(), resources.NewNativeNetworkAccess())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Service absent entirely (empty mapping set) — still a hard error on the
	// strict path.
	absentService := &basev0.Configuration{
		Origin: resources.ConfigurationWorkspace,
		Infos: []*basev0.ConfigurationInformation{
			{
				Name:                "work-context",
				ConfigurationValues: []*basev0.ConfigurationValue{{Key: "authority-jwks-url", Value: "${endpoint:saas/frontend/http}"}},
			},
		},
	}
	_, err = resources.InterpolateConfigurationEndpoints(ctx, absentService, nil, resources.NewNativeNetworkAccess())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// A run-wide configuration is interpolated for every service, including leaf infra
// services that do not depend on the referenced endpoint and therefore have it
// absent from their mapping set. Such a value is not for that consumer: it is
// dropped rather than failing the service, while sibling values with no reference
// survive.
func TestInterpolateRunWideConfigurationEndpointsDropsReferenceAbsentFromConsumer(t *testing.T) {
	ctx := context.Background()
	conf := &basev0.Configuration{
		Origin: resources.ConfigurationWorkspace,
		Infos: []*basev0.ConfigurationInformation{
			{
				Name: "work-context",
				ConfigurationValues: []*basev0.ConfigurationValue{
					{Key: "authority-jwks-url", Value: "${endpoint:saas/frontend/http}/v1/auth/.well-known/jwks.json"},
					{Key: "static", Value: "keep-me"},
				},
			},
		},
	}

	// A leaf service that depends on nothing has an empty mapping set.
	resolved, err := resources.InterpolateRunWideConfigurationEndpoints(ctx, conf, nil, resources.NewNativeNetworkAccess())
	require.NoError(t, err)

	dropped, err := resources.GetConfigurationValue(ctx, resolved, "work-context", "authority-jwks-url")
	require.NoError(t, err)
	assert.Empty(t, dropped)

	static, err := resources.GetConfigurationValue(ctx, resolved, "work-context", "static")
	require.NoError(t, err)
	assert.Equal(t, "keep-me", static)
}

// The run-wide drop is per endpoint reference, not per service. A consumer that
// depends on saas-starter/auth-sidecar for its http endpoint, given a run-wide
// value referencing that service's grpc endpoint (which it does not depend on),
// has the value dropped — not hard-failed. The service-granularity heuristic would
// have failed this consumer's boot on a config it never consumes.
func TestInterpolateRunWideConfigurationEndpointsDropsSiblingEndpointOfDependedService(t *testing.T) {
	ctx := context.Background()
	conf := &basev0.Configuration{
		Origin: resources.ConfigurationWorkspace,
		Infos: []*basev0.ConfigurationInformation{
			{
				Name: "work-context",
				ConfigurationValues: []*basev0.ConfigurationValue{
					{Key: "grpc-url", Value: "${endpoint:saas-starter/auth-sidecar/grpc}"},
					{Key: "http-url", Value: "${endpoint:saas-starter/auth-sidecar/http}/v1/jwks"},
				},
			},
		},
	}

	// gatewayMappings() gives the consumer only the http endpoint of auth-sidecar.
	resolved, err := resources.InterpolateRunWideConfigurationEndpoints(ctx, conf, gatewayMappings(), resources.NewNativeNetworkAccess())
	require.NoError(t, err)

	// The sibling grpc reference is dropped; the http reference it does depend on
	// resolves and survives.
	dropped, err := resources.GetConfigurationValue(ctx, resolved, "work-context", "grpc-url")
	require.NoError(t, err)
	assert.Empty(t, dropped)

	url, err := resources.GetConfigurationValue(ctx, resolved, "work-context", "http-url")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:1234/v1/jwks", url)
}
