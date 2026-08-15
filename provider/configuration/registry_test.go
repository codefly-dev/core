package configuration_test

import (
	"crypto/sha256"
	"fmt"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
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
		configuration.ProductAnalyticsBrowserContract,
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
	require.NoError(t, registry.Validate(configuration.BillingContract, validBillingRuntimeValues(), validationContext(configuration.ConsumerRuntime)))
	require.NoError(t, registry.Validate(configuration.BillingContract, validBillingBrowserValues(), validationContext(configuration.ConsumerBrowser)))
}

func TestContractsRejectUnsafePurposeClassificationConsumerAndBrowserMixing(t *testing.T) {
	registry := configuration.NewRegistry()

	t.Run("undeclared key", func(t *testing.T) {
		values := validBillingRuntimeValues()
		values["STRIPE_ACCOUNT_ID"] = publicValue("acct_123", configuration.PurposeNone, configuration.ConsumerRuntime, configuration.BrowserDenied)
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values, validationContext(configuration.ConsumerRuntime)), "undeclared key")
	})
	t.Run("secret shaped browser value", func(t *testing.T) {
		values := validBillingBrowserValues()
		value := values["STRIPE_PUBLISHABLE_KEY"]
		value.String = "sk_live_1234567890abcdef"
		values["STRIPE_PUBLISHABLE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values, validationContext(configuration.ConsumerBrowser)), "secret-shaped")
	})
	t.Run("management purpose in runtime", func(t *testing.T) {
		values := validBillingRuntimeValues()
		value := values["STRIPE_SECRET_KEY"]
		value.CredentialPurpose = configuration.PurposeManagement
		values["STRIPE_SECRET_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values, validationContext(configuration.ConsumerRuntime)), "credential purpose")
	})
	t.Run("classification weakening", func(t *testing.T) {
		values := validBillingRuntimeValues()
		value := values["STRIPE_SECRET_KEY"]
		value.Classification = configuration.ClassificationPublic
		values["STRIPE_SECRET_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values, validationContext(configuration.ConsumerRuntime)), "weaken classification")
	})
	t.Run("browser exposure above ceiling", func(t *testing.T) {
		values := validBillingRuntimeValues()
		value := values["STRIPE_SECRET_KEY"]
		value.BrowserExposure = configuration.BrowserAllowed
		values["STRIPE_SECRET_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values, validationContext(configuration.ConsumerRuntime)), "browser exposure")
	})
	t.Run("build credential in runtime contract", func(t *testing.T) {
		values := validBillingRuntimeValues()
		values["SENTRY_AUTH_TOKEN"] = opaqueValue("secret://sentry/build", configuration.PurposeBuild, configuration.ConsumerBuild)
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values, validationContext(configuration.ConsumerRuntime)), "undeclared key")
	})
	t.Run("missing provenance", func(t *testing.T) {
		values := validBillingRuntimeValues()
		value := values["STRIPE_SECRET_KEY"]
		delete(value.Provenance, configuration.ProvenanceArtifact)
		values["STRIPE_SECRET_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values, validationContext(configuration.ConsumerRuntime)), "artifact provenance")
	})
	t.Run("raw literal in opaque reference", func(t *testing.T) {
		values := validBillingRuntimeValues()
		value := values["STRIPE_SECRET_KEY"]
		value.OpaqueReference = "sk_live_1234567890abcdef"
		values["STRIPE_SECRET_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values, validationContext(configuration.ConsumerRuntime)), "opaque reference")

		value.OpaqueReference = "secret://sk_live_1234567890abcdef"
		values["STRIPE_SECRET_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.BillingContract, values, validationContext(configuration.ConsumerRuntime)), "identifier is invalid")
	})
}

func TestBuildOnlyErrorTrackingCredentialUsesDistinctContract(t *testing.T) {
	registry := configuration.NewRegistry()
	build := map[string]configuration.Value{
		"SENTRY_AUTH_TOKEN": opaqueValue("secret://sentry/build", configuration.PurposeBuild, configuration.ConsumerBuild),
		"SENTRY_ORG":        publicValue("codefly", configuration.PurposeNone, configuration.ConsumerBuild, configuration.BrowserDenied),
		"SENTRY_PROJECT":    publicValue("api", configuration.PurposeNone, configuration.ConsumerBuild, configuration.BrowserDenied),
	}
	require.NoError(t, registry.Validate(configuration.ErrorTrackingBuildContract, build, validationContext(configuration.ConsumerBuild)))
	require.Error(t, registry.Validate(configuration.ErrorTrackingContract, build, validationContext(configuration.ConsumerBuild)))
}

func TestBrowserExposureMayTightenBelowSchemaCeiling(t *testing.T) {
	registry := configuration.NewRegistry()
	value := publicValue("https://public@example.invalid/1", configuration.PurposeRuntime, configuration.ConsumerBrowser, configuration.BrowserDenied)
	require.NoError(t, registry.Validate(configuration.ErrorTrackingContract, map[string]configuration.Value{
		"SENTRY_DSN": value,
	}, validationContext(configuration.ConsumerBrowser)))
}

func TestFeatureFlagsContractsSeparateServerAndBrowserConsumers(t *testing.T) {
	registry := configuration.NewRegistry()
	require.NoError(t, registry.Validate(configuration.FeatureFlagsContract, validFeatureFlagsServerValues(), validationContext(configuration.ConsumerRuntime)))
	require.NoError(t, registry.Validate(configuration.FeatureFlagsBrowserContract, validFeatureFlagsBrowserValues(), validationContext(configuration.ConsumerBrowser)))

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
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsContract, values, validationContext(configuration.ConsumerRuntime)), "undeclared key")
	})
	t.Run("management purpose on server credential", func(t *testing.T) {
		values := validFeatureFlagsServerValues()
		value := values["FEATURE_FLAGS_SERVER_CREDENTIAL"]
		value.CredentialPurpose = configuration.PurposeManagement
		values["FEATURE_FLAGS_SERVER_CREDENTIAL"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsContract, values, validationContext(configuration.ConsumerRuntime)), "credential purpose")
	})
	t.Run("secret browser credential", func(t *testing.T) {
		values := validFeatureFlagsBrowserValues()
		value := values["FEATURE_FLAGS_BROWSER_CREDENTIAL"]
		value.Classification = configuration.ClassificationSecret
		values["FEATURE_FLAGS_BROWSER_CREDENTIAL"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsBrowserContract, values, validationContext(configuration.ConsumerBrowser)), "classification exceeds schema ceiling")
	})
	t.Run("weakened browser credential classification", func(t *testing.T) {
		values := validFeatureFlagsBrowserValues()
		value := values["FEATURE_FLAGS_BROWSER_CREDENTIAL"]
		value.Classification = configuration.ClassificationPublic
		values["FEATURE_FLAGS_BROWSER_CREDENTIAL"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsBrowserContract, values, validationContext(configuration.ConsumerBrowser)), "weaken classification")
	})
	t.Run("server credential assigned to browser", func(t *testing.T) {
		values := validFeatureFlagsServerValues()
		value := values["FEATURE_FLAGS_SERVER_CREDENTIAL"]
		value.Consumer = configuration.ConsumerBrowser
		values["FEATURE_FLAGS_SERVER_CREDENTIAL"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsContract, values, validationContext(configuration.ConsumerRuntime)), "consumer")
	})
	t.Run("browser credential assigned to backend", func(t *testing.T) {
		values := validFeatureFlagsBrowserValues()
		value := values["FEATURE_FLAGS_BROWSER_CREDENTIAL"]
		value.Consumer = configuration.ConsumerRuntime
		values["FEATURE_FLAGS_BROWSER_CREDENTIAL"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsBrowserContract, values, validationContext(configuration.ConsumerBrowser)), "consumer")
	})
	t.Run("noncanonical credential reference", func(t *testing.T) {
		values := validFeatureFlagsBrowserValues()
		value := values["FEATURE_FLAGS_BROWSER_CREDENTIAL"]
		value.OpaqueReference = "secret://feature-flags/browser?version=1"
		values["FEATURE_FLAGS_BROWSER_CREDENTIAL"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsBrowserContract, values, validationContext(configuration.ConsumerBrowser)), "canonical host reference")
	})
	t.Run("raw server URL", func(t *testing.T) {
		values := validFeatureFlagsServerValues()
		value := values["FEATURE_FLAGS_SERVER_ENDPOINT"]
		value.String = "http://169.254.169.254"
		values["FEATURE_FLAGS_SERVER_ENDPOINT"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsContract, values, validationContext(configuration.ConsumerRuntime)), "endpoint reference")
	})
	t.Run("noncanonical endpoint reference", func(t *testing.T) {
		values := validFeatureFlagsBrowserValues()
		value := values["FEATURE_FLAGS_EDGE_ENDPOINT"]
		value.String = "endpoint://feature-flags/edge?"
		values["FEATURE_FLAGS_EDGE_ENDPOINT"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsBrowserContract, values, validationContext(configuration.ConsumerBrowser)), "endpoint reference")
	})
	t.Run("provider supplied endpoint", func(t *testing.T) {
		values := validFeatureFlagsServerValues()
		value := values["FEATURE_FLAGS_SERVER_ENDPOINT"]
		value.MutatedBy = configuration.MutatorProvider
		values["FEATURE_FLAGS_SERVER_ENDPOINT"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsContract, values, validationContext(configuration.ConsumerRuntime)), "not provider mutable")
	})
	t.Run("endpoint without host provenance", func(t *testing.T) {
		values := validFeatureFlagsServerValues()
		value := values["FEATURE_FLAGS_SERVER_ENDPOINT"]
		delete(value.Provenance, configuration.ProvenanceHost)
		values["FEATURE_FLAGS_SERVER_ENDPOINT"] = value
		require.ErrorContains(t, registry.Validate(configuration.FeatureFlagsContract, values, validationContext(configuration.ConsumerRuntime)), "host provenance")
	})
}

func TestProductAnalyticsContractSeparatesBrowserAndServerCapture(t *testing.T) {
	registry := configuration.NewRegistry()
	require.NoError(t, registry.Validate(configuration.ProductAnalyticsContract, validProductAnalyticsServerValues(), validationContext(configuration.ConsumerRuntime)))
	require.NoError(t, registry.Validate(configuration.ProductAnalyticsBrowserContract, validProductAnalyticsBrowserValues(), validationContext(configuration.ConsumerBrowser)))

	serverContract, err := registry.Lookup(configuration.ProductAnalyticsContract)
	require.NoError(t, err)
	browserContract, err := registry.Lookup(configuration.ProductAnalyticsBrowserContract)
	require.NoError(t, err)

	serverKeys := []string{
		"PRODUCT_ANALYTICS_MODE",
		"PRODUCT_ANALYTICS_PROJECT_ID",
		"PRODUCT_ANALYTICS_ENVIRONMENT",
		"PRODUCT_ANALYTICS_SERVER_CAPTURE_ORIGIN",
		"PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY",
	}
	browserKeys := []string{
		"PRODUCT_ANALYTICS_MODE",
		"PRODUCT_ANALYTICS_PROJECT_ID",
		"PRODUCT_ANALYTICS_ENVIRONMENT",
		"PRODUCT_ANALYTICS_BROWSER_CAPTURE_ORIGIN",
		"PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY",
	}
	require.Len(t, serverContract.Keys, len(serverKeys))
	for _, name := range serverKeys {
		require.Contains(t, serverContract.Keys, name)
		require.Equal(t, configuration.ConsumerRuntime, serverContract.Keys[name].Consumer)
	}
	require.Len(t, browserContract.Keys, len(browserKeys))
	for _, name := range browserKeys {
		require.Contains(t, browserContract.Keys, name)
		require.Equal(t, configuration.ConsumerBrowser, browserContract.Keys[name].Consumer)
	}

	browser := browserContract.Keys["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"]
	require.Equal(t, configuration.ClassificationPublic, browser.ClassificationFloor)
	require.Equal(t, configuration.ClassificationPublic, browser.ClassificationCeiling)
	require.Equal(t, configuration.PurposeRuntime, browser.CredentialPurpose)
	require.Equal(t, configuration.BrowserAllowed, browser.BrowserExposure)
	require.Equal(t, configuration.ConsumerBrowser, browser.Consumer)

	server := serverContract.Keys["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"]
	require.Equal(t, configuration.ValueOpaqueReference, server.Type)
	require.Equal(t, configuration.ClassificationSecret, server.ClassificationFloor)
	require.Equal(t, configuration.PurposeRuntime, server.CredentialPurpose)
	require.Equal(t, configuration.BrowserDenied, server.BrowserExposure)
	require.Equal(t, configuration.ConsumerRuntime, server.Consumer)

	for contractID, contract := range map[string]configuration.Contract{
		configuration.ProductAnalyticsContract:        serverContract,
		configuration.ProductAnalyticsBrowserContract: browserContract,
	} {
		for name, key := range contract.Keys {
			require.True(t, key.HostMutable, contractID+" "+name)
			require.NotEqual(t, configuration.PurposeManagement, key.CredentialPurpose, contractID+" "+name)
			require.Contains(t, key.RequiredProvenance, configuration.ProvenanceProvider, contractID+" "+name)
			require.Contains(t, key.RequiredProvenance, configuration.ProvenanceBinding, contractID+" "+name)
			require.Contains(t, key.RequiredProvenance, configuration.ProvenanceArtifact, contractID+" "+name)
			if key.Type == configuration.ValueEndpointReference || key.Type == configuration.ValueOpaqueReference {
				require.Contains(t, key.RequiredProvenance, configuration.ProvenanceHost, contractID+" "+name)
			}
			if key.Type == configuration.ValueEndpointReference {
				require.False(t, key.ProviderMutable, contractID+" "+name)
			}
			if key.Consumer == configuration.ConsumerBrowser {
				require.NotEqual(t, configuration.ClassificationSecret, key.ClassificationCeiling, contractID+" "+name)
			}
		}
	}
}

func TestProductAnalyticsContractRejectsUnsafeProjection(t *testing.T) {
	registry := configuration.NewRegistry()

	t.Run("personal key in browser capture", func(t *testing.T) {
		values := validProductAnalyticsBrowserValues()
		value := values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"]
		value.String = "phx_personal_management_key"
		values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsBrowserContract, values, validationContext(configuration.ConsumerBrowser)), "secret-shaped")
	})
	t.Run("management purpose in browser capture", func(t *testing.T) {
		values := validProductAnalyticsBrowserValues()
		value := values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"]
		value.CredentialPurpose = configuration.PurposeManagement
		values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsBrowserContract, values, validationContext(configuration.ConsumerBrowser)), "credential purpose")
	})
	t.Run("secret browser classification", func(t *testing.T) {
		values := validProductAnalyticsBrowserValues()
		value := values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"]
		value.Classification = configuration.ClassificationSecret
		values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsBrowserContract, values, validationContext(configuration.ConsumerBrowser)), "classification exceeds")
	})
	t.Run("server capture exposed to browser", func(t *testing.T) {
		values := validProductAnalyticsServerValues()
		value := values["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"]
		value.BrowserExposure = configuration.BrowserAllowed
		values["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, values, validationContext(configuration.ConsumerRuntime)), "browser exposure")
	})
	t.Run("server capture replaced by management authority", func(t *testing.T) {
		values := validProductAnalyticsServerValues()
		value := values["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"]
		value.CredentialPurpose = configuration.PurposeManagement
		values["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, values, validationContext(configuration.ConsumerRuntime)), "credential purpose")
	})
	t.Run("management reference relabeled as runtime", func(t *testing.T) {
		values := validProductAnalyticsServerValues()
		value := values["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"]
		value.OpaqueReference = "secret://analytics/management"
		value.SafeFingerprint = fingerprint("secret://analytics/management")
		value.Provenance[configuration.ProvenanceHost] = value.SafeFingerprint
		values["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, values, validationContext(configuration.ConsumerRuntime)), "host-attested purpose")
	})
	t.Run("raw origin outside host endpoint admission", func(t *testing.T) {
		values := validProductAnalyticsServerValues()
		value := values["PRODUCT_ANALYTICS_SERVER_CAPTURE_ORIGIN"]
		value.String = "https://unadmitted.example:443"
		values["PRODUCT_ANALYTICS_SERVER_CAPTURE_ORIGIN"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, values, validationContext(configuration.ConsumerRuntime)), "endpoint reference")
	})
	t.Run("origin without host provenance", func(t *testing.T) {
		values := validProductAnalyticsServerValues()
		value := values["PRODUCT_ANALYTICS_SERVER_CAPTURE_ORIGIN"]
		delete(value.Provenance, configuration.ProvenanceHost)
		values["PRODUCT_ANALYTICS_SERVER_CAPTURE_ORIGIN"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, values, validationContext(configuration.ConsumerRuntime)), "host provenance")
	})
	t.Run("opaque reference with mismatched fingerprint", func(t *testing.T) {
		values := validProductAnalyticsServerValues()
		value := values["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"]
		value.SafeFingerprint = fingerprint("different-secret")
		value.Provenance[configuration.ProvenanceHost] = value.SafeFingerprint
		values["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"] = value
		require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, values, validationContext(configuration.ConsumerRuntime)), "safe fingerprint")
	})
}

func TestProductAnalyticsConsumerConfigurationsAreIndependent(t *testing.T) {
	registry := configuration.NewRegistry()
	require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, validProductAnalyticsServerValues(), configuration.ValidationContext{}), "validation consumer")

	browserValues := validProductAnalyticsBrowserValues()
	browserValues["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"] = opaqueValue("secret://analytics/server", configuration.PurposeRuntime, configuration.ConsumerRuntime)
	require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsBrowserContract, browserValues, validationContext(configuration.ConsumerBrowser)), "undeclared key")

	serverValues := validProductAnalyticsServerValues()
	serverValues["PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY"] = publicValue("phc_public_project_capture_key", configuration.PurposeRuntime, configuration.ConsumerBrowser, configuration.BrowserAllowed)
	require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, serverValues, validationContext(configuration.ConsumerRuntime)), "undeclared key")
}

func TestProductAnalyticsOutputProposalsRemainConsumerScoped(t *testing.T) {
	registry := configuration.NewRegistry()
	require.NoError(t, registry.ValidateProposal(productAnalyticsServerProposal()))
	require.NoError(t, registry.ValidateProposal(productAnalyticsBrowserProposal()))

	browser := productAnalyticsBrowserProposal()
	browser.Values["PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY"] = outputOpaque("secret://analytics/server", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME)
	require.ErrorContains(t, registry.ValidateProposal(browser), "undeclared key")
}

func TestProductAnalyticsBrowserCaptureIsAllOrNothing(t *testing.T) {
	registry := configuration.NewRegistry()
	for _, missing := range []string{
		"PRODUCT_ANALYTICS_BROWSER_CAPTURE_ORIGIN",
		"PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY",
	} {
		t.Run(missing, func(t *testing.T) {
			values := validProductAnalyticsBrowserValues()
			delete(values, missing)
			require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsBrowserContract, values, validationContext(configuration.ConsumerBrowser)), "required key")
		})
	}
}

func TestProductAnalyticsPrivacyAuthorityIsNotRuntimeConfiguration(t *testing.T) {
	registry := configuration.NewRegistry()
	for _, name := range []string{
		"PRODUCT_ANALYTICS_MANAGEMENT_API_KEY",
		"PRODUCT_ANALYTICS_PERSONAL_API_KEY",
		"PRODUCT_ANALYTICS_PRIVACY_DELETION_KEY",
	} {
		t.Run("undeclared "+name, func(t *testing.T) {
			values := validProductAnalyticsServerValues()
			values[name] = opaqueValue("secret://analytics/management", configuration.PurposeManagement, configuration.ConsumerRuntime)
			require.ErrorContains(t, registry.Validate(configuration.ProductAnalyticsContract, values, validationContext(configuration.ConsumerRuntime)), "undeclared key")
		})
	}
}

func TestProductAnalyticsContractContainsOnlyProductAnalyticsKeys(t *testing.T) {
	registry := configuration.NewRegistry()
	for _, contractID := range []string{configuration.ProductAnalyticsContract, configuration.ProductAnalyticsBrowserContract} {
		contract, err := registry.Lookup(contractID)
		require.NoError(t, err)
		for name := range contract.Keys {
			for _, forbidden := range []string{"FEATURE_FLAG", "ERROR_TRACKING", "SENTRY", "APM", "OTEL", "TRACE"} {
				require.NotContains(t, name, forbidden)
			}
		}
	}
}

func TestProductAnalyticsOriginsUseHostEndpointReferences(t *testing.T) {
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
			values := validProductAnalyticsBrowserValues()
			value := values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_ORIGIN"]
			value.String = origin
			values["PRODUCT_ANALYTICS_BROWSER_CAPTURE_ORIGIN"] = value
			require.Error(t, registry.Validate(configuration.ProductAnalyticsBrowserContract, values, validationContext(configuration.ConsumerBrowser)))
		})
	}
}

func TestProductAnalyticsAcceptsHostEndpointReferences(t *testing.T) {
	registry := configuration.NewRegistry()
	require.NoError(t, registry.Validate(configuration.ProductAnalyticsContract, validProductAnalyticsServerValues(), validationContext(configuration.ConsumerRuntime)))
	require.NoError(t, registry.Validate(configuration.ProductAnalyticsBrowserContract, validProductAnalyticsBrowserValues(), validationContext(configuration.ConsumerBrowser)))
}

func TestPostHogCaptureAndManagementKeyShapesRemainDistinct(t *testing.T) {
	require.False(t, configuration.LooksSecret("phc_public_project_capture_key"))
	require.True(t, configuration.LooksSecret("phx_personal_management_key"))
	require.True(t, configuration.LooksSecret("PHX_PERSONAL_MANAGEMENT_KEY"))
}

func validBillingRuntimeValues() map[string]configuration.Value {
	return map[string]configuration.Value{
		"STRIPE_SECRET_KEY":     opaqueValue("secret://stripe/runtime", configuration.PurposeRuntime, configuration.ConsumerRuntime),
		"STRIPE_WEBHOOK_SECRET": opaqueValue("secret://stripe/webhook", configuration.PurposeWebhookVerification, configuration.ConsumerRuntime),
	}
}

func productAnalyticsServerProposal() *providerv0.OutputProposal {
	return &providerv0.OutputProposal{
		Contract: configuration.ProductAnalyticsContract,
		Values: map[string]*providerv0.OutputValue{
			"PRODUCT_ANALYTICS_MODE":                  outputString("posthog"),
			"PRODUCT_ANALYTICS_PROJECT_ID":            outputString("12345"),
			"PRODUCT_ANALYTICS_ENVIRONMENT":           outputString("production"),
			"PRODUCT_ANALYTICS_SERVER_CAPTURE_ORIGIN": outputString("endpoint://analytics/server"),
			"PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY":    outputOpaque("secret://analytics/server", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME),
		},
	}
}

func productAnalyticsBrowserProposal() *providerv0.OutputProposal {
	return &providerv0.OutputProposal{
		Contract: configuration.ProductAnalyticsBrowserContract,
		Values: map[string]*providerv0.OutputValue{
			"PRODUCT_ANALYTICS_MODE":                   outputString("posthog"),
			"PRODUCT_ANALYTICS_PROJECT_ID":             outputString("12345"),
			"PRODUCT_ANALYTICS_ENVIRONMENT":            outputString("production"),
			"PRODUCT_ANALYTICS_BROWSER_CAPTURE_ORIGIN": outputString("endpoint://analytics/browser"),
			"PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY":    outputString("phc_public_project_capture_key"),
		},
	}
}

func outputString(value string) *providerv0.OutputValue {
	return &providerv0.OutputValue{Kind: &providerv0.OutputValue_PublicValue{PublicValue: &providerv0.PublicValue{
		Kind: &providerv0.PublicValue_StringValue{StringValue: value},
	}}}
}

func outputOpaque(reference string, purpose providerv0.CredentialPurpose) *providerv0.OutputValue {
	return &providerv0.OutputValue{Kind: &providerv0.OutputValue_OpaqueReference{OpaqueReference: &providerv0.OpaqueReference{
		Reference: reference, Purpose: purpose, SafeFingerprint: fingerprint(reference),
	}}}
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

func validBillingBrowserValues() map[string]configuration.Value {
	return map[string]configuration.Value{
		"STRIPE_PUBLISHABLE_KEY": publicValue("pk_live_public_identifier", configuration.PurposeRuntime, configuration.ConsumerBrowser, configuration.BrowserAllowed),
	}
}

func validProductAnalyticsServerValues() map[string]configuration.Value {
	return map[string]configuration.Value{
		"PRODUCT_ANALYTICS_MODE":                  publicValue("posthog", configuration.PurposeNone, configuration.ConsumerRuntime, configuration.BrowserDenied),
		"PRODUCT_ANALYTICS_PROJECT_ID":            publicValue("12345", configuration.PurposeNone, configuration.ConsumerRuntime, configuration.BrowserDenied),
		"PRODUCT_ANALYTICS_ENVIRONMENT":           publicValue("production", configuration.PurposeNone, configuration.ConsumerRuntime, configuration.BrowserDenied),
		"PRODUCT_ANALYTICS_SERVER_CAPTURE_ORIGIN": endpointValue("endpoint://analytics/server", configuration.ConsumerRuntime, configuration.BrowserDenied),
		"PRODUCT_ANALYTICS_SERVER_CAPTURE_KEY":    opaqueValue("secret://analytics/server", configuration.PurposeRuntime, configuration.ConsumerRuntime),
	}
}

func validProductAnalyticsBrowserValues() map[string]configuration.Value {
	return map[string]configuration.Value{
		"PRODUCT_ANALYTICS_MODE":                   publicValue("posthog", configuration.PurposeNone, configuration.ConsumerBrowser, configuration.BrowserAllowed),
		"PRODUCT_ANALYTICS_PROJECT_ID":             publicValue("12345", configuration.PurposeNone, configuration.ConsumerBrowser, configuration.BrowserAllowed),
		"PRODUCT_ANALYTICS_ENVIRONMENT":            publicValue("production", configuration.PurposeNone, configuration.ConsumerBrowser, configuration.BrowserAllowed),
		"PRODUCT_ANALYTICS_BROWSER_CAPTURE_ORIGIN": endpointValue("endpoint://analytics/browser", configuration.ConsumerBrowser, configuration.BrowserAllowed),
		"PRODUCT_ANALYTICS_BROWSER_CAPTURE_KEY":    publicValue("phc_public_project_capture_key", configuration.PurposeRuntime, configuration.ConsumerBrowser, configuration.BrowserAllowed),
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
	fingerprint := fingerprint(reference)
	provenance := provenance()
	provenance[configuration.ProvenanceHost] = fingerprint
	return configuration.Value{
		Type: configuration.ValueOpaqueReference, OpaqueReference: reference, SafeFingerprint: fingerprint,
		Classification: configuration.ClassificationSecret, CredentialPurpose: purpose,
		BrowserExposure: configuration.BrowserDenied, Consumer: consumer, MutatedBy: configuration.MutatorProvider,
		Provenance: provenance,
	}
}

func validHostAttestations() configuration.HostAttestations {
	return configuration.HostAttestations{
		OpaqueReferences: []*providerv0.OpaqueReference{
			opaqueAttestation("secret://stripe/runtime", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME),
			opaqueAttestation("secret://stripe/webhook", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_WEBHOOK_VERIFICATION),
			opaqueAttestation("secret://sentry/build", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_BUILD),
			opaqueAttestation("secret://feature-flags/server", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME),
			opaqueAttestation("secret://feature-flags/browser", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME),
			opaqueAttestation("secret://analytics/server", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME),
			opaqueAttestation("secret://analytics/management", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT),
		},
	}
}

func validationContext(consumer configuration.ConsumerClass) configuration.ValidationContext {
	return configuration.ValidationContext{Consumer: consumer, Attestations: validHostAttestations()}
}

func opaqueAttestation(reference string, purpose providerv0.CredentialPurpose) *providerv0.OpaqueReference {
	return &providerv0.OpaqueReference{Reference: reference, Purpose: purpose, SafeFingerprint: fingerprint(reference)}
}

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", sum)
}

func provenance() map[configuration.Provenance]string {
	return map[configuration.Provenance]string{
		configuration.ProvenanceProvider: "codefly.dev/provider-fixture:1.2.3",
		configuration.ProvenanceBinding:  "workspace/environment/binding",
		configuration.ProvenanceArtifact: "sha256:artifact",
	}
}
