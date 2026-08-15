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
		configuration.ProductAnalyticsContract,
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

func TestProductAnalyticsContractSeparatesBrowserAndServerCapture(t *testing.T) {
	registry := configuration.NewRegistry()
	require.NoError(t, registry.Validate(configuration.ProductAnalyticsContract, validProductAnalyticsValues()))

	contract, err := registry.Lookup(configuration.ProductAnalyticsContract)
	require.NoError(t, err)
	expectedKeys := []string{
		"PRODUCT_ANALYTICS_MODE",
		"PRODUCT_ANALYTICS_PROJECT_ID",
		"PRODUCT_ANALYTICS_ENVIRONMENT",
		"PRODUCT_ANALYTICS_BROWSER_CAPTURE_ORIGIN",
		"PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY",
		"PRODUCT_ANALYTICS_SERVER_CAPTURE_ORIGIN",
		"PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY",
	}
	require.Len(t, contract.Keys, len(expectedKeys))
	for _, name := range expectedKeys {
		require.Contains(t, contract.Keys, name)
	}
	browser := contract.Keys["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"]
	require.Equal(t, configuration.ClassificationPublic, browser.ClassificationFloor)
	require.Equal(t, configuration.ClassificationPublic, browser.ClassificationCeiling)
	require.Equal(t, configuration.PurposeRuntime, browser.CredentialPurpose)
	require.Equal(t, configuration.BrowserAllowed, browser.BrowserExposure)
	require.Equal(t, configuration.ConsumerBrowser, browser.Consumer)

	server := contract.Keys["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"]
	require.Equal(t, configuration.ValueOpaqueReference, server.Type)
	require.Equal(t, configuration.ClassificationSecret, server.ClassificationFloor)
	require.Equal(t, configuration.PurposeRuntime, server.CredentialPurpose)
	require.Equal(t, configuration.BrowserDenied, server.BrowserExposure)
	require.Equal(t, configuration.ConsumerRuntime, server.Consumer)

	for name, key := range contract.Keys {
		require.True(t, key.ProviderMutable, name)
		require.True(t, key.HostMutable, name)
		require.NotEqual(t, configuration.PurposeManagement, key.CredentialPurpose, name)
		require.Equal(t, []configuration.Provenance{
			configuration.ProvenanceProvider,
			configuration.ProvenanceBinding,
			configuration.ProvenanceArtifact,
		}, key.RequiredProvenance, name)
		if key.Consumer == configuration.ConsumerBrowser {
			require.NotEqual(t, configuration.ClassificationSecret, key.ClassificationCeiling, name)
		}
	}
}

func TestProductAnalyticsContractRejectsUnsafeProjection(t *testing.T) {
	registry := configuration.NewRegistry()

	t.Run("personal key in browser capture", func(t *testing.T) {
		values := validProductAnalyticsValues()
		value := values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"]
		value.String = "phx_personal_management_key"
		values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, values), "secret-shaped")
	})
	t.Run("management purpose in browser capture", func(t *testing.T) {
		values := validProductAnalyticsValues()
		value := values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"]
		value.CredentialPurpose = configuration.PurposeManagement
		values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, values), "credential purpose")
	})
	t.Run("secret browser classification", func(t *testing.T) {
		values := validProductAnalyticsValues()
		value := values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"]
		value.Classification = configuration.ClassificationSecret
		values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, values), "classification exceeds")
	})
	t.Run("server capture exposed to browser", func(t *testing.T) {
		values := validProductAnalyticsValues()
		value := values["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"]
		value.BrowserExposure = configuration.BrowserAllowed
		values["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, values), "browser exposure")
	})
	t.Run("server capture replaced by management authority", func(t *testing.T) {
		values := validProductAnalyticsValues()
		value := values["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"]
		value.CredentialPurpose = configuration.PurposeManagement
		values["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, values), "credential purpose")
	})
}

func TestProductAnalyticsPrivacyAuthorityIsNotRuntimeConfiguration(t *testing.T) {
	registry := configuration.NewRegistry()
	for _, name := range []string{
		"PRODUCT_ANALYTICS_MANAGEMENT_API_KEY",
		"PRODUCT_ANALYTICS_PERSONAL_API_KEY",
		"PRODUCT_ANALYTICS_PRIVACY_DELETION_KEY",
	} {
		t.Run("undeclared "+name, func(t *testing.T) {
			values := validProductAnalyticsValues()
			values[name] = opaqueValue("secret://analytics/management", configuration.PurposeManagement, configuration.ConsumerRuntime)
			require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, values), "undeclared key")
		})
	}
}

func TestProductAnalyticsContractContainsOnlyProductAnalyticsKeys(t *testing.T) {
	contract, err := configuration.NewRegistry().Lookup(configuration.ProductAnalyticsContract)
	require.NoError(t, err)
	for name := range contract.Keys {
		for _, forbidden := range []string{"FEATURE_FLAG", "ERROR_TRACKING", "SENTRY", "APM", "OTEL", "TRACE"} {
			require.NotContains(t, name, forbidden)
		}
	}
}

func TestProductAnalyticsOriginsMustBeCanonical(t *testing.T) {
	canonical, err := configuration.CanonicalOrigin("HTTPS://EU.I.POSTHOG.COM:443/")
	require.NoError(t, err)
	require.Equal(t, "https://eu.i.posthog.com", canonical)
	canonical, err = configuration.CanonicalOrigin("HTTP://[0:0:0:0:0:0:0:1]:8080/")
	require.NoError(t, err)
	require.Equal(t, "http://[::1]:8080", canonical)

	registry := configuration.NewRegistry()
	for _, origin := range []string{
		"HTTPS://eu.i.posthog.com",
		"https://eu.i.posthog.com/",
		"https://eu.i.posthog.com:443",
		"https://eu.i.posthog.com/batch",
		"https://eu.i.posthog.com?api_key=public",
		"https://user@eu.i.posthog.com",
	} {
		t.Run(origin, func(t *testing.T) {
			values := validProductAnalyticsValues()
			value := values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_ORIGIN"]
			value.String = origin
			values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_ORIGIN"] = value
			require.Error(t, registry.Validate(configuration.ProductAnalyticsContract, values))
		})
	}
}

func TestPostHogCaptureAndManagementKeyShapesRemainDistinct(t *testing.T) {
	require.False(t, configuration.LooksSecret("phc_public_project_capture_key"))
	require.True(t, configuration.LooksSecret("phx_personal_management_key"))
	require.True(t, configuration.LooksSecret("PHX_PERSONAL_MANAGEMENT_KEY"))
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

func validProductAnalyticsValues() map[string]configuration.Value {
	return map[string]configuration.Value{
		"PRODUCT_ANALYTICS_MODE":                   publicValue("posthog", configuration.PurposeNone, configuration.ConsumerRuntime, configuration.BrowserDenied),
		"PRODUCT_ANALYTICS_PROJECT_ID":             publicValue("12345", configuration.PurposeNone, configuration.ConsumerRuntime, configuration.BrowserDenied),
		"PRODUCT_ANALYTICS_ENVIRONMENT":            publicValue("production", configuration.PurposeNone, configuration.ConsumerRuntime, configuration.BrowserDenied),
		"PRODUCT_ANALYTICS_BROWSER_CAPTURE_ORIGIN": originValue("https://eu.i.posthog.com", configuration.ConsumerBrowser, configuration.BrowserAllowed),
		"PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY":    publicValue("phc_public_project_capture_key", configuration.PurposeRuntime, configuration.ConsumerBrowser, configuration.BrowserAllowed),
		"PRODUCT_ANALYTICS_SERVER_CAPTURE_ORIGIN":  originValue("https://eu.i.posthog.com", configuration.ConsumerRuntime, configuration.BrowserDenied),
		"PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY":     opaqueValue("capture://analytics/server", configuration.PurposeRuntime, configuration.ConsumerRuntime),
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

func originValue(value string, consumer configuration.ConsumerClass, browser configuration.BrowserExposure) configuration.Value {
	return configuration.Value{
		Type: configuration.ValueOrigin, String: value,
		Classification: configuration.ClassificationPublic, CredentialPurpose: configuration.PurposeNone,
		BrowserExposure: browser, Consumer: consumer, MutatedBy: configuration.MutatorProvider,
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
