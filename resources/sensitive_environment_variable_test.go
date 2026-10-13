package resources_test

import (
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// A carrier's name embeds module, service, endpoint and configuration-group
// names. A service called auth-gateway must not turn its endpoint address into
// a credential, a public group called datasource-oauth must not turn its
// providers list into one, while a credential-named key stays sensitive
// wherever it rides.
func TestSensitiveEnvironmentVariableIgnoresCarrierStructure(t *testing.T) {
	public := []string{
		// Public values of a configuration GROUP whose name trips the OAUTH
		// word: the exact carriers a host render refused as credentials.
		"CODEFLY__WORKSPACE_CONFIGURATION__DATASOURCE_OAUTH__PROVIDERS",
		"CODEFLY__WORKSPACE_CONFIGURATION__DATASOURCE_OAUTH__CLIENT_ID",
		"CODEFLY__SERVICE_CONFIGURATION__SAAS__AUTH_GATEWAY__DATASOURCE_OAUTH__PROVIDERS",
		"CODEFLY__SERVICE_CONFIGURATION__SAAS__AUTH_GATEWAY__DATASOURCE_OAUTH__CLIENT_ID",
		// A group name is structure even when it IS a credential word: the
		// header value of a public group called auth is whatever its key says,
		// and a credential declared there rides the secret carrier instead.
		"CODEFLY__SERVICE_CONFIGURATION__APP__SVC__AUTH__HEADER",
		// A key may carry a double underscore of its own; it is still the key.
		"CODEFLY__WORKSPACE_CONFIGURATION__DATASOURCE_OAUTH__PROVIDERS__LIST",
		// Endpoint addresses of a service whose name trips the AUTH word.
		"CODEFLY__ENDPOINT__SAAS__AUTH_GATEWAY__GRPC__GRPC",
		"CODEFLY__ENDPOINT__SAAS__AUTH_GATEWAY__REST__REST",
		"CODEFLY__SELF_ENDPOINT__SAAS__AUTH_GATEWAY__GRPC__GRPC",
		"CODEFLY__REST_ROUTE__SAAS__AUTH_GATEWAY__REST__REST___V1__SESSIONS____GET",
		// Endpoint and API names are structure too.
		"CODEFLY__ENDPOINT__STORE__SESSION_STORE__CONNECTION__TCP",
		// A public operator prefix in front of an endpoint carrier.
		"NEXT_PUBLIC_CODEFLY__ENDPOINT__SAAS__AUTH_GATEWAY__REST__REST",
		// Configuration of a service whose name trips the AUTH word.
		"CODEFLY__SERVICE_CONFIGURATION__SAAS__AUTH_GATEWAY__IDENTITY__AUTHORITY_ISSUER",
		"CODEFLY__SERVICE_CONFIGURATION__SAAS__AUTH_GATEWAY__IDENTITY__AUDIENCE",
		"CODEFLY__WORKSPACE_CONFIGURATION__MODEL_INSTALLATION__MAX_OUTPUT_TOKENS",
		"CODEFLY__CONFIGURATION_DOCUMENT_V1__0123456789abcdef",
		"AUTHORITY_ISSUER",
		"MAX_OUTPUT_TOKENS",
	}
	for _, name := range public {
		require.Falsef(t, resources.IsSensitiveEnvironmentVariable(name), "%s carries no credential", name)
	}

	secret := []string{
		"JWT_SECRET",
		"WEBHOOK_SECRET",
		"ACCESS_TOKEN",
		"CLIENT_SECRET",
		"DATABASE_PASSWORD",
		"API_KEY",
		"CODEFLY_INTERNAL_TOKEN",
		// A credential key inside a service configuration carrier, whatever
		// the service is called.
		"CODEFLY__SERVICE_CONFIGURATION__SAAS__AUTH_GATEWAY__IDENTITY__CLIENT_SECRET",
		"CODEFLY__SERVICE_CONFIGURATION__APP__SVC__WEBHOOK__WEBHOOK_SECRET",
		"CODEFLY__WORKSPACE_CONFIGURATION__IDENTITY__ACCESS_TOKEN",
		// A raw credential key in the group that tripped the OAUTH word.
		"CODEFLY__WORKSPACE_CONFIGURATION__DATASOURCE_OAUTH__CLIENT_SECRET",
		"CODEFLY__WORKSPACE_CONFIGURATION__DATASOURCE_OAUTH__ACCESS_TOKEN",
		"CODEFLY__SERVICE_CONFIGURATION__SAAS__AUTH_GATEWAY__DATASOURCE_OAUTH__CLIENT_SECRET",
		"CODEFLY__WORKSPACE_CONFIGURATION__DATASOURCE_OAUTH__PROVIDERS__TOKEN",
		// The same public keys under the secret carriers of that group.
		"CODEFLY__WORKSPACE_SECRET_CONFIGURATION__DATASOURCE_OAUTH__PROVIDERS",
		"CODEFLY__SERVICE_SECRET_CONFIGURATION__SAAS__AUTH_GATEWAY__DATASOURCE_OAUTH__CLIENT_ID",
		// Secret carriers are secret whatever the key inside is called.
		"CODEFLY__SERVICE_SECRET_CONFIGURATION__SAAS__AUTH_GATEWAY__IDENTITY__AUTHORITY_ISSUER",
		"CODEFLY__WORKSPACE_SECRET_CONFIGURATION__MODEL_INSTALLATION__MAX_OUTPUT_TOKENS",
		"CODEFLY__SECRET_CONFIGURATION_DOCUMENT_V1__0123456789abcdef",
		// A credential-named operator prefix is still classified.
		"ACCESS_TOKEN_CODEFLY__ENDPOINT__SAAS__AUTH_GATEWAY__REST__REST",
	}
	for _, name := range secret {
		require.Truef(t, resources.IsSensitiveEnvironmentVariable(name), "%s must stay sensitive", name)
	}
}

// The carrier names the classifier recognises are the ones the environment
// manager actually produces.
func TestSensitiveEnvironmentVariableMatchesProducedCarriers(t *testing.T) {
	info := &resources.EndpointInformation{Module: "saas", Service: "auth-gateway", Name: "grpc", API: "grpc"}
	require.Equal(t, "CODEFLY__ENDPOINT__SAAS__AUTH_GATEWAY__GRPC__GRPC", resources.EndpointAsEnvironmentVariableKey(info))
	require.False(t, resources.IsSensitiveEnvironmentVariable(resources.EndpointAsEnvironmentVariableKey(info)))
	require.False(t, resources.IsSensitiveEnvironmentVariable(resources.SelfEndpointAsEnvironmentVariableKey(info)))

	require.False(t, resources.IsSensitiveEnvironmentVariable(
		resources.ServiceConfigurationKeyFromUnique("saas/auth-gateway", "identity", "authority-issuer")))
	require.False(t, resources.IsSensitiveEnvironmentVariable(
		resources.ServiceConfigurationKeyFromUnique("saas/auth-gateway", "datasource-oauth", "providers")))
	require.True(t, resources.IsSensitiveEnvironmentVariable(
		resources.ServiceConfigurationKeyFromUnique("saas/auth-gateway", "datasource-oauth", "client-secret")))
	workspace := &basev0.Configuration{Origin: resources.ConfigurationWorkspace}
	for key, sensitive := range map[string]bool{"providers": false, "client-id": false, "client-secret": true} {
		carrier := resources.ConfigurationValueEnvironmentKey(workspace, "datasource-oauth", &basev0.ConfigurationValue{Key: key})
		require.Equal(t, "CODEFLY__WORKSPACE_CONFIGURATION__DATASOURCE_OAUTH__"+resources.NameToKey(key), carrier)
		require.Equal(t, sensitive, resources.IsSensitiveEnvironmentVariable(carrier), carrier)
		secret := resources.ConfigurationValueEnvironmentKey(workspace, "datasource-oauth", &basev0.ConfigurationValue{Key: key, Secret: true})
		require.True(t, resources.IsSensitiveEnvironmentVariable(secret), secret)
	}
	require.True(t, resources.IsSensitiveEnvironmentVariable(
		resources.ServiceConfigurationKeyFromUnique("saas/auth-gateway", "identity", "client-secret")))
	require.True(t, resources.IsSensitiveEnvironmentVariable(
		resources.ServiceSecretConfigurationKeyFromUnique("saas/auth-gateway", "identity", "authority-issuer")))

	require.False(t, resources.IsSensitiveEnvironmentVariable(resources.ConfigurationDocumentKey("saas/auth-gateway", "identity", "staging", false)))
	require.True(t, resources.IsSensitiveEnvironmentVariable(resources.ConfigurationDocumentKey("saas/auth-gateway", "identity", "staging", true)))
}
