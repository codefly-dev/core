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
	require.NoError(t, resources.InterpolateConfigurationEndpoints(ctx, conf, gatewayMappings(), resources.NewNativeNetworkAccess()))

	url, err := resources.GetConfigurationValue(ctx, conf, "work-context", "authority-jwks-url")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:1234/v1/auth/.well-known/jwks.json", url)

	static, err := resources.GetConfigurationValue(ctx, conf, "work-context", "static")
	require.NoError(t, err)
	assert.Equal(t, "keep-me", static)
}
