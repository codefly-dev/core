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

func validBillingValues() map[string]configuration.Value {
	return map[string]configuration.Value{
		"STRIPE_PUBLISHABLE_KEY": publicValue("pk_live_public_identifier", configuration.PurposeRuntime, configuration.ConsumerBrowser, configuration.BrowserAllowed),
		"STRIPE_SECRET_KEY":      opaqueValue("secret://stripe/runtime", configuration.PurposeRuntime, configuration.ConsumerRuntime),
		"STRIPE_WEBHOOK_SECRET":  opaqueValue("secret://stripe/webhook", configuration.PurposeWebhookVerification, configuration.ConsumerRuntime),
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
