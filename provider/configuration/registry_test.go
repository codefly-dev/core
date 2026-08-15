package configuration_test

import (
	"testing"

	"github.com/codefly-dev/core/provider/configuration"
	"github.com/stretchr/testify/require"
)

func TestRegistrySeedsVersionedGenericContracts(t *testing.T) {
	registry := configuration.NewRegistry()
	require.Equal(t, []string{
		configuration.BillingContract,
		configuration.EmailContract,
		configuration.ErrorTrackingBuildContract,
		configuration.ErrorTrackingContract,
		configuration.FeatureFlagsBrowserContract,
		configuration.FeatureFlagsContract,
	}, registry.Contracts())
	for _, id := range registry.Contracts() {
		contract, err := registry.Lookup(id)
		require.NoError(t, err)
		require.NotEmpty(t, contract.Keys)
	}
}

func TestBillingContractAcceptsPurposeBoundOpaqueRuntimeReferences(t *testing.T) {
	registry := configuration.NewRegistry()
	require.NoError(t, registry.Validate(configuration.BillingContract, validBillingValues()))
}

func TestContractsRejectUnsafePurposeClassificationConsumerAndBrowserMixing(t *testing.T) {
	registry := configuration.NewRegistry()

	t.Run("undeclared key", func(t *testing.T) {
		values := validBillingValues()
		values["STRIPE_ACCOUNT_ID"] = publicValue("acct_123", configuration.PurposeNone, configuration.ConsumerRuntime, configuration.BrowserDenied)
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values), "undeclared key")
	})
	t.Run("secret shaped browser value", func(t *testing.T) {
		values := validBillingValues()
		value := values["STRIPE_PUBLISHABLE_KEY"]
		value.String = "sk_live_1234567890abcdef"
		values["STRIPE_PUBLISHABLE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values), "secret-shaped")
	})
	t.Run("management purpose in runtime", func(t *testing.T) {
		values := validBillingValues()
		value := values["STRIPE_SECRET_KEY"]
		value.CredentialPurpose = configuration.PurposeManagement
		values["STRIPE_SECRET_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values), "credential purpose")
	})
	t.Run("classification weakening", func(t *testing.T) {
		values := validBillingValues()
		value := values["STRIPE_SECRET_KEY"]
		value.Classification = configuration.ClassificationPublic
		values["STRIPE_SECRET_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values), "weaken classification")
	})
	t.Run("browser exposure above ceiling", func(t *testing.T) {
		values := validBillingValues()
		value := values["STRIPE_SECRET_KEY"]
		value.BrowserExposure = configuration.BrowserAllowed
		values["STRIPE_SECRET_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values), "browser exposure")
	})
	t.Run("build credential in runtime contract", func(t *testing.T) {
		values := validBillingValues()
		values["SENTRY_AUTH_TOKEN"] = opaqueValue("secret://sentry/build", configuration.PurposeBuild, configuration.ConsumerBuild)
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values), "undeclared key")
	})
	t.Run("missing provenance", func(t *testing.T) {
		values := validBillingValues()
		value := values["STRIPE_SECRET_KEY"]
		delete(value.Provenance, configuration.ProvenanceArtifact)
		values["STRIPE_SECRET_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values), "artifact provenance")
	})
	t.Run("raw literal in opaque reference", func(t *testing.T) {
		values := validBillingValues()
		value := values["STRIPE_SECRET_KEY"]
		value.OpaqueReference = "sk_live_1234567890abcdef"
		values["STRIPE_SECRET_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values), "opaque reference")

		value.OpaqueReference = "secret://sk_live_1234567890abcdef"
		values["STRIPE_SECRET_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values), "identifier is invalid")
	})
}

func TestBuildOnlyErrorTrackingCredentialUsesDistinctContract(t *testing.T) {
	registry := configuration.NewRegistry()
	build := map[string]configuration.Value{
		"SENTRY_AUTH_TOKEN": opaqueValue("secret://sentry/build", configuration.PurposeBuild, configuration.ConsumerBuild),
		"SENTRY_ORG":        publicValue("codefly", configuration.PurposeNone, configuration.ConsumerBuild, configuration.BrowserDenied),
		"SENTRY_PROJECT":    publicValue("api", configuration.PurposeNone, configuration.ConsumerBuild, configuration.BrowserDenied),
	}
	require.NoError(t, registry.Validate(configuration.ErrorTrackingBuildContract, build))
	require.Error(t, registry.Validate(configuration.ErrorTrackingContract, build))
}

func TestBrowserExposureMayTightenBelowSchemaCeiling(t *testing.T) {
	registry := configuration.NewRegistry()
	value := publicValue("https://public@example.invalid/1", configuration.PurposeRuntime, configuration.ConsumerBrowser, configuration.BrowserDenied)
	require.NoError(t, registry.Validate(configuration.ErrorTrackingContract, map[string]configuration.Value{
		"SENTRY_DSN": value,
	}))
}

func TestFeatureFlagsContractsSeparateServerAndBrowserConsumers(t *testing.T) {
	registry := configuration.NewRegistry()
	require.NoError(t, registry.Validate(configuration.FeatureFlagsContract, validFeatureFlagsServerValues()))
	require.NoError(t, registry.Validate(configuration.FeatureFlagsBrowserContract, validFeatureFlagsBrowserValues()))

	server, err := registry.Lookup(configuration.FeatureFlagsContract)
	require.NoError(t, err)
	browser, err := registry.Lookup(configuration.FeatureFlagsBrowserContract)
	require.NoError(t, err)
	require.NotContains(t, server.Keys, "FEATURE_FLAGS_BROWSER_CREDENTIAL")
	require.NotContains(t, browser.Keys, "FEATURE_FLAGS_SERVER_CREDENTIAL")
	for name, key := range server.Keys {
		require.Equal(t, configuration.ConsumerRuntime, key.Consumer, name)
	}
	for name, key := range browser.Keys {
		require.Equal(t, configuration.ConsumerBrowser, key.Consumer, name)
	}
	require.Contains(t, server.Keys, "FEATURE_FLAGS_APPLICATION_ID")
	require.Contains(t, server.Keys, "FEATURE_FLAGS_ENVIRONMENT_ID")
	require.Contains(t, server.Keys, "FEATURE_FLAGS_PROVIDER_MODE")
	require.Contains(t, browser.Keys, "FEATURE_FLAGS_APPLICATION_ID")
	require.Contains(t, browser.Keys, "FEATURE_FLAGS_ENVIRONMENT_ID")
	require.Contains(t, browser.Keys, "FEATURE_FLAGS_PROVIDER_MODE")

	serverEndpoint := server.Keys["FEATURE_FLAGS_SERVER_ENDPOINT"]
	require.Equal(t, configuration.ValueEndpointReference, serverEndpoint.Type)
	require.False(t, serverEndpoint.ProviderMutable)
	require.True(t, serverEndpoint.HostMutable)
	require.Contains(t, serverEndpoint.RequiredProvenance, configuration.ProvenanceHost)
}

func TestFeatureFlagsContractsRejectUnsafeRuntimeProjection(t *testing.T) {
	registry := configuration.NewRegistry()

	t.Run("undeclared management credential", func(t *testing.T) {
		values := validFeatureFlagsServerValues()
		values["FEATURE_FLAGS_MANAGEMENT_CREDENTIAL"] = opaqueValue("secret://feature-flags/management", configuration.PurposeManagement, configuration.ConsumerRuntime)
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsContract, values), "undeclared key")
	})
	t.Run("management purpose on server credential", func(t *testing.T) {
		values := validFeatureFlagsServerValues()
		value := values["FEATURE_FLAGS_SERVER_CREDENTIAL"]
		value.CredentialPurpose = configuration.PurposeManagement
		values["FEATURE_FLAGS_SERVER_CREDENTIAL"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsContract, values), "credential purpose")
	})
	t.Run("secret browser credential", func(t *testing.T) {
		values := validFeatureFlagsBrowserValues()
		value := values["FEATURE_FLAGS_BROWSER_CREDENTIAL"]
		value.Classification = configuration.ClassificationSecret
		values["FEATURE_FLAGS_BROWSER_CREDENTIAL"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsBrowserContract, values), "classification exceeds schema ceiling")
	})
	t.Run("weakened browser credential classification", func(t *testing.T) {
		values := validFeatureFlagsBrowserValues()
		value := values["FEATURE_FLAGS_BROWSER_CREDENTIAL"]
		value.Classification = configuration.ClassificationPublic
		values["FEATURE_FLAGS_BROWSER_CREDENTIAL"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsBrowserContract, values), "weaken classification")
	})
	t.Run("server credential assigned to browser", func(t *testing.T) {
		values := validFeatureFlagsServerValues()
		value := values["FEATURE_FLAGS_SERVER_CREDENTIAL"]
		value.Consumer = configuration.ConsumerBrowser
		values["FEATURE_FLAGS_SERVER_CREDENTIAL"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsContract, values), "consumer")
	})
	t.Run("browser credential assigned to backend", func(t *testing.T) {
		values := validFeatureFlagsBrowserValues()
		value := values["FEATURE_FLAGS_BROWSER_CREDENTIAL"]
		value.Consumer = configuration.ConsumerRuntime
		values["FEATURE_FLAGS_BROWSER_CREDENTIAL"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsBrowserContract, values), "consumer")
	})
	t.Run("noncanonical credential reference", func(t *testing.T) {
		values := validFeatureFlagsBrowserValues()
		value := values["FEATURE_FLAGS_BROWSER_CREDENTIAL"]
		value.OpaqueReference = "secret://feature-flags/browser?version=1"
		values["FEATURE_FLAGS_BROWSER_CREDENTIAL"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsBrowserContract, values), "canonical host reference")
	})
	t.Run("raw server URL", func(t *testing.T) {
		values := validFeatureFlagsServerValues()
		value := values["FEATURE_FLAGS_SERVER_ENDPOINT"]
		value.String = "http://169.254.169.254"
		values["FEATURE_FLAGS_SERVER_ENDPOINT"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsContract, values), "endpoint reference")
	})
	t.Run("noncanonical endpoint reference", func(t *testing.T) {
		values := validFeatureFlagsBrowserValues()
		value := values["FEATURE_FLAGS_EDGE_ENDPOINT"]
		value.String = "endpoint://feature-flags/edge?"
		values["FEATURE_FLAGS_EDGE_ENDPOINT"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsBrowserContract, values), "endpoint reference")
	})
	t.Run("provider supplied endpoint", func(t *testing.T) {
		values := validFeatureFlagsServerValues()
		value := values["FEATURE_FLAGS_SERVER_ENDPOINT"]
		value.MutatedBy = configuration.MutatorProvider
		values["FEATURE_FLAGS_SERVER_ENDPOINT"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsContract, values), "not provider mutable")
	})
	t.Run("endpoint without host provenance", func(t *testing.T) {
		values := validFeatureFlagsServerValues()
		value := values["FEATURE_FLAGS_SERVER_ENDPOINT"]
		delete(value.Provenance, configuration.ProvenanceHost)
		values["FEATURE_FLAGS_SERVER_ENDPOINT"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsContract, values), "host provenance")
	})
}

func validBillingValues() map[string]configuration.Value {
	return map[string]configuration.Value{
		"STRIPE_PUBLISHABLE_KEY": publicValue("pk_live_public_identifier", configuration.PurposeRuntime, configuration.ConsumerBrowser, configuration.BrowserAllowed),
		"STRIPE_SECRET_KEY":      opaqueValue("secret://stripe/runtime", configuration.PurposeRuntime, configuration.ConsumerRuntime),
		"STRIPE_WEBHOOK_SECRET":  opaqueValue("secret://stripe/webhook", configuration.PurposeWebhookVerification, configuration.ConsumerRuntime),
	}
}

func validFeatureFlagsServerValues() map[string]configuration.Value {
	return map[string]configuration.Value{
		"FEATURE_FLAGS_SERVER_ENDPOINT":   endpointValue("endpoint://feature-flags/server", configuration.ConsumerRuntime, configuration.BrowserDenied),
		"FEATURE_FLAGS_APPLICATION_ID":    publicValue("accounts", configuration.PurposeNone, configuration.ConsumerRuntime, configuration.BrowserDenied),
		"FEATURE_FLAGS_ENVIRONMENT_ID":    publicValue("production", configuration.PurposeNone, configuration.ConsumerRuntime, configuration.BrowserDenied),
		"FEATURE_FLAGS_PROVIDER_MODE":     publicValue("hosted", configuration.PurposeNone, configuration.ConsumerRuntime, configuration.BrowserDenied),
		"FEATURE_FLAGS_SERVER_CREDENTIAL": opaqueValue("secret://feature-flags/server", configuration.PurposeRuntime, configuration.ConsumerRuntime),
	}
}

func validFeatureFlagsBrowserValues() map[string]configuration.Value {
	browserCredential := opaqueValue("secret://feature-flags/browser", configuration.PurposeRuntime, configuration.ConsumerBrowser)
	browserCredential.Classification = configuration.ClassificationSensitive
	browserCredential.BrowserExposure = configuration.BrowserAllowed
	return map[string]configuration.Value{
		"FEATURE_FLAGS_EDGE_ENDPOINT":      endpointValue("endpoint://feature-flags/edge", configuration.ConsumerBrowser, configuration.BrowserAllowed),
		"FEATURE_FLAGS_APPLICATION_ID":     publicValue("accounts", configuration.PurposeNone, configuration.ConsumerBrowser, configuration.BrowserAllowed),
		"FEATURE_FLAGS_ENVIRONMENT_ID":     publicValue("production", configuration.PurposeNone, configuration.ConsumerBrowser, configuration.BrowserAllowed),
		"FEATURE_FLAGS_PROVIDER_MODE":      publicValue("hosted", configuration.PurposeNone, configuration.ConsumerBrowser, configuration.BrowserAllowed),
		"FEATURE_FLAGS_BROWSER_CREDENTIAL": browserCredential,
	}
}

func endpointValue(reference string, consumer configuration.ConsumerClass, browser configuration.BrowserExposure) configuration.Value {
	valueProvenance := provenance()
	valueProvenance[configuration.ProvenanceHost] = "endpoint-admission:sha256:origin"
	return configuration.Value{
		Type: configuration.ValueEndpointReference, String: reference,
		Classification: configuration.ClassificationPublic, CredentialPurpose: configuration.PurposeNone,
		BrowserExposure: browser, Consumer: consumer, MutatedBy: configuration.MutatorHost,
		Provenance: valueProvenance,
	}
}

func publicValue(value string, purpose configuration.CredentialPurpose, consumer configuration.ConsumerClass, browser configuration.BrowserExposure) configuration.Value {
	return configuration.Value{
		Type: configuration.ValueString, String: value,
		Classification: configuration.ClassificationPublic, CredentialPurpose: purpose,
		BrowserExposure: browser, Consumer: consumer, MutatedBy: configuration.MutatorProvider,
		Provenance: provenance(),
	}
}

func opaqueValue(reference string, purpose configuration.CredentialPurpose, consumer configuration.ConsumerClass) configuration.Value {
	return configuration.Value{
		Type: configuration.ValueOpaqueReference, OpaqueReference: reference,
		Classification: configuration.ClassificationSecret, CredentialPurpose: purpose,
		BrowserExposure: configuration.BrowserDenied, Consumer: consumer, MutatedBy: configuration.MutatorProvider,
		Provenance: provenance(),
	}
}

func provenance() map[configuration.Provenance]string {
	return map[configuration.Provenance]string{
		configuration.ProvenanceProvider: "codefly.dev/provider-fixture:1.2.3",
		configuration.ProvenanceBinding:  "workspace/environment/binding",
		configuration.ProvenanceArtifact: "sha256:artifact",
	}
}
