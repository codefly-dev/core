package sdk_test

import (
	"context"
	"strings"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/canonical"
	"github.com/codefly-dev/core/provider/sdk"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestProjectOutputActionIsValidationReady(t *testing.T) {
	proposal := billingProposal()
	action, err := sdk.NewProjectOutputAction("emit", 0, proposal)
	require.NoError(t, err)
	require.NotNil(t, action.GetOutput())

	// The action must be accepted by canonical plan validation as-is, without
	// the caller reaching in to attach a bound output proposal.
	plan := &providerv0.OrderedPlan{PlanId: "plan", Actions: []*providerv0.PlanAction{action}}
	_, err = canonical.BindOrderedPlanDigest(plan)
	require.NoError(t, err)

	// A missing proposal fails fast at construction rather than surfacing later
	// as a plan-digest validation error.
	_, err = sdk.NewProjectOutputAction("emit", 0, nil)
	require.ErrorContains(t, err, "output proposal")
}

func TestTypedActionsAndOutputProposal(t *testing.T) {
	_, err := sdk.NewCreateAction("create", 0, "account", "")
	require.ErrorContains(t, err, "prospective")
	action, err := sdk.NewCreateAction("create", 0, "account", "prospective-123")
	require.NoError(t, err)
	require.Equal(t, providerv0.ActionType_ACTION_TYPE_CREATE, action.Type)

	proposal := billingProposal()
	bound, err := sdk.BindOutputProposal(proposal)
	require.NoError(t, err)
	repeated, err := sdk.BindOutputProposal(bound)
	require.NoError(t, err)
	require.Equal(t, bound.Digest, repeated.Digest)

	proposal.Values["STRIPE_SECRET_KEY"].GetOpaqueReference().Reference = "sk_live_1234567890abcdef"
	_, err = sdk.BindOutputProposal(proposal)
	require.ErrorContains(t, err, "opaque reference")
}

func TestFeatureFlagsOutputProposalEnforcesConsumerAndAdmissionBoundaries(t *testing.T) {
	for _, proposal := range []*providerv0.OutputProposal{
		featureFlagsServerProposal(),
		featureFlagsBrowserProposal(),
	} {
		_, err := sdk.BindOutputProposal(proposal)
		require.NoError(t, err)
	}

	tests := map[string]struct {
		proposal *providerv0.OutputProposal
		want     string
	}{
		"missing required server value": {
			proposal: featureFlagsServerProposalWithout("FEATURE_FLAGS_APPLICATION_ID"),
			want:     "required key",
		},
		"undeclared management credential": {
			proposal: withOutputValue(featureFlagsServerProposal(), "FEATURE_FLAGS_MANAGEMENT_CREDENTIAL",
				opaqueOutput("secret://feature-flags/management", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT)),
			want: "undeclared key",
		},
		"management purpose on server credential": {
			proposal: withOutputValue(featureFlagsServerProposal(), "FEATURE_FLAGS_SERVER_CREDENTIAL",
				opaqueOutput("secret://feature-flags/server", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT)),
			want: "credential purpose",
		},
		"raw server URL instead of admitted endpoint reference": {
			proposal: withOutputValue(featureFlagsServerProposal(), "FEATURE_FLAGS_SERVER_ENDPOINT", publicOutput("http://169.254.169.254")),
			want:     "endpoint reference",
		},
		"server credential in browser contract": {
			proposal: withOutputValue(featureFlagsBrowserProposal(), "FEATURE_FLAGS_SERVER_CREDENTIAL",
				opaqueOutput("secret://feature-flags/server", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME)),
			want: "undeclared key",
		},
		"browser credential in server contract": {
			proposal: withOutputValue(featureFlagsServerProposal(), "FEATURE_FLAGS_BROWSER_CREDENTIAL",
				opaqueOutput("secret://feature-flags/browser", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME)),
			want: "undeclared key",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := sdk.BindOutputProposal(test.proposal)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestFilteredResponseAndCaptureHelpersExposeOnlySafeValuesOrReferences(t *testing.T) {
	response := &providerv0.ExecuteRequestResponse{
		Delivery:  providerv0.DeliveryState_DELIVERY_STATE_RESPONSE_RECEIVED,
		Certainty: providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE,
		Forwarded: []*providerv0.FilteredField{
			{Selector: "$.id", Value: &providerv0.PublicValue{Kind: &providerv0.PublicValue_StringValue{StringValue: "remote-123"}}},
		},
	}
	decoded, err := sdk.DecodeFilteredResponse(response)
	require.NoError(t, err)
	require.Equal(t, "remote-123", decoded["$.id"].GetStringValue())

	capture := &providerv0.CaptureResult{
		CaptureId: "capture-1", Captured: true,
		SinkReference: &providerv0.OpaqueReference{
			Reference: "capture://sink/1", Purpose: providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
		},
	}
	reference, err := sdk.HandleCaptureResult(capture)
	require.NoError(t, err)
	require.Equal(t, "capture://sink/1", reference.Reference)
	capture.Captured = false
	_, err = sdk.HandleCaptureResult(capture)
	require.ErrorContains(t, err, "not durable")

	capture.Captured = true
	capture.SinkReference.Reference = "sk_live_1234567890abcdef"
	_, err = sdk.HandleCaptureResult(capture)
	require.ErrorContains(t, err, "opaque reference")

	capture.SinkReference.Reference = "capture://sink/1"
	capture.SinkReference.Purpose = providerv0.CredentialPurpose(99)
	_, err = sdk.HandleCaptureResult(capture)
	require.ErrorContains(t, err, "unknown credential purpose")
}

func TestFilteredResponseRejectsUnknownDeliveryAndCertaintyEnums(t *testing.T) {
	response := &providerv0.ExecuteRequestResponse{
		Delivery:  providerv0.DeliveryState(99),
		Certainty: providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE,
	}
	_, err := sdk.DecodeFilteredResponse(response)
	require.ErrorContains(t, err, "delivery is unknown")

	response.Delivery = providerv0.DeliveryState_DELIVERY_STATE_NOT_SENT
	response.Certainty = providerv0.OutcomeCertainty(99)
	_, err = sdk.DecodeFilteredResponse(response)
	require.ErrorContains(t, err, "certainty is unknown")
}

func TestProviderInformationBootstrapsCatalogFromVerifiedArtifactIdentity(t *testing.T) {
	digest := "sha256:" + strings.Repeat("1", 64)
	information := &providerv0.GetProviderInformationResponse{
		Artifact: &providerv0.AgentArtifactIdentity{
			Publisher: "codefly.dev", Name: "fixture", Version: "1.2.3",
			ArtifactDigest: digest, ManifestDigest: digest,
		},
		Catalog: &providerv0.RuntimeCatalog{Digest: "sha256:" + strings.Repeat("2", 64)},
	}
	base, err := sdk.NewBase(information)
	require.NoError(t, err)
	response, err := base.GetProviderInformation(context.Background(), &providerv0.GetProviderInformationRequest{
		Artifact: information.Artifact,
	})
	require.NoError(t, err)
	require.Equal(t, information.Catalog.Digest, response.Catalog.Digest)

	mismatch := &providerv0.AgentArtifactIdentity{
		Publisher: "codefly.dev", Name: "fixture", Version: "1.2.4",
		ArtifactDigest: digest, ManifestDigest: digest,
	}
	_, err = base.GetProviderInformation(context.Background(), &providerv0.GetProviderInformationRequest{Artifact: mismatch})
	require.ErrorContains(t, err, "artifact identity mismatch")
}

func TestValidateUpgradeRequiresOneExactOfflineStep(t *testing.T) {
	digest := "sha256:" + strings.Repeat("1", 64)
	upgraded := &providerv0.ProviderStateV1{
		StateSchemaVersion: 1, Generation: 1,
		Binding:    &providerv0.BindingAddress{WorkspaceId: "workspace", EnvironmentId: "environment", BindingId: "binding"},
		ProviderId: "codefly.dev/fixture", ProviderVersion: "1.2.3",
		ManifestSchemaVersion: "codefly.provider-manifest/v0", ManifestDigest: digest, ArtifactDigest: digest,
		Ownership: providerv0.Ownership_OWNERSHIP_OBSERVED,
	}
	response := &providerv0.UpgradeStateResponse{
		State: &providerv0.ProviderState{
			SchemaVersion: 1,
			Schema:        &providerv0.ProviderState_V1{V1: upgraded},
		},
		Record: &providerv0.UpgradeRecord{
			FromVersion: 0, ToVersion: 1, AgentVersion: "2.0.0",
			ArtifactDigest: digest, PriorDigest: digest, ResultDigest: digest, UpgradedAt: timestamppb.Now(),
		},
	}
	require.NoError(t, sdk.ValidateUpgrade(response, 0, 1))
	require.ErrorContains(t, sdk.ValidateUpgrade(response, 0, 2), "exactly one")

	response.State.SchemaVersion = 2
	response.Record.FromVersion = 1
	response.Record.ToVersion = 2
	require.ErrorContains(t, sdk.ValidateUpgrade(response, 1, 2), "does not match v1 payload")
}

func featureFlagsServerProposal() *providerv0.OutputProposal {
	return &providerv0.OutputProposal{
		Contract: "codefly.dev/configuration/feature-flags@1", TargetGeneration: 1,
		Values: map[string]*providerv0.OutputValue{
			"FEATURE_FLAGS_SERVER_ENDPOINT":   publicOutput("endpoint://feature-flags/server"),
			"FEATURE_FLAGS_APPLICATION_ID":    publicOutput("accounts"),
			"FEATURE_FLAGS_ENVIRONMENT_ID":    publicOutput("production"),
			"FEATURE_FLAGS_PROVIDER_MODE":     publicOutput("hosted"),
			"FEATURE_FLAGS_SERVER_CREDENTIAL": opaqueOutput("secret://feature-flags/server", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME),
		},
	}
}

func billingProposal() *providerv0.OutputProposal {
	return &providerv0.OutputProposal{
		Contract: "codefly.dev/configuration/billing@1", TargetGeneration: 2,
		Values: map[string]*providerv0.OutputValue{
			"STRIPE_PUBLISHABLE_KEY": publicOutput("pk_live_public_identifier"),
			"STRIPE_SECRET_KEY":      opaqueOutput("secret://stripe/runtime", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME),
			"STRIPE_WEBHOOK_SECRET":  opaqueOutput("secret://stripe/webhook", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_WEBHOOK_VERIFICATION),
		},
	}
}

func featureFlagsBrowserProposal() *providerv0.OutputProposal {
	return &providerv0.OutputProposal{
		Contract: "codefly.dev/configuration/feature-flags-browser@1", TargetGeneration: 1,
		Values: map[string]*providerv0.OutputValue{
			"FEATURE_FLAGS_EDGE_ENDPOINT":      publicOutput("endpoint://feature-flags/edge"),
			"FEATURE_FLAGS_APPLICATION_ID":     publicOutput("accounts"),
			"FEATURE_FLAGS_ENVIRONMENT_ID":     publicOutput("production"),
			"FEATURE_FLAGS_PROVIDER_MODE":      publicOutput("hosted"),
			"FEATURE_FLAGS_BROWSER_CREDENTIAL": opaqueOutput("secret://feature-flags/browser", providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME),
		},
	}
}

func featureFlagsServerProposalWithout(key string) *providerv0.OutputProposal {
	proposal := featureFlagsServerProposal()
	delete(proposal.Values, key)
	return proposal
}

func withOutputValue(proposal *providerv0.OutputProposal, key string, value *providerv0.OutputValue) *providerv0.OutputProposal {
	proposal.Values[key] = value
	return proposal
}

func publicOutput(value string) *providerv0.OutputValue {
	return &providerv0.OutputValue{Kind: &providerv0.OutputValue_PublicValue{PublicValue: &providerv0.PublicValue{
		Kind: &providerv0.PublicValue_StringValue{StringValue: value},
	}}}
}

func opaqueOutput(reference string, purpose providerv0.CredentialPurpose) *providerv0.OutputValue {
	return &providerv0.OutputValue{Kind: &providerv0.OutputValue_OpaqueReference{OpaqueReference: &providerv0.OpaqueReference{
		Reference: reference, Purpose: purpose,
	}}}
}
