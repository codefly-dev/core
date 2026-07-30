package sdk_test

import (
	"strings"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/sdk"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestTypedActionsAndOutputProposal(t *testing.T) {
	_, err := sdk.NewCreateAction("create", 0, "account", "")
	require.ErrorContains(t, err, "prospective")
	action, err := sdk.NewCreateAction("create", 0, "account", "prospective-123")
	require.NoError(t, err)
	require.Equal(t, providerv0.ActionType_ACTION_TYPE_CREATE, action.Type)

	proposal := &providerv0.OutputProposal{
		Contract: "codefly.dev/configuration/billing@1", TargetGeneration: 2,
		Values: map[string]*providerv0.OutputValue{
			"STRIPE_SECRET_KEY": {
				Kind: &providerv0.OutputValue_OpaqueReference{
					OpaqueReference: &providerv0.OpaqueReference{
						Reference: "secret://stripe/runtime", Purpose: providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
					},
				},
			},
		},
	}
	bound, err := sdk.BindOutputProposal(proposal)
	require.NoError(t, err)
	repeated, err := sdk.BindOutputProposal(bound)
	require.NoError(t, err)
	require.Equal(t, bound.Digest, repeated.Digest)
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
}

func TestProviderBaseRejectsCatalogOrArtifactMismatch(t *testing.T) {
	digest := "sha256:" + strings.Repeat("1", 64)
	_, err := sdk.NewBase(&providerv0.GetProviderInformationResponse{
		Artifact: &providerv0.AgentArtifactIdentity{
			ArtifactDigest: digest, ManifestDigest: digest, CatalogDigest: digest,
		},
		Catalog: &providerv0.RuntimeCatalog{Digest: "sha256:" + strings.Repeat("2", 64)},
	})
	require.ErrorContains(t, err, "catalog digest mismatch")
}

func TestValidateUpgradeRequiresOneExactOfflineStep(t *testing.T) {
	digest := "sha256:" + strings.Repeat("1", 64)
	response := &providerv0.UpgradeStateResponse{
		State: &providerv0.ProviderStateV1{StateSchemaVersion: 2},
		Record: &providerv0.UpgradeRecord{
			FromVersion: 1, ToVersion: 2, AgentVersion: "2.0.0",
			ArtifactDigest: digest, PriorDigest: digest, ResultDigest: digest, UpgradedAt: timestamppb.Now(),
		},
	}
	require.NoError(t, sdk.ValidateUpgrade(response, 1, 2))
	require.ErrorContains(t, sdk.ValidateUpgrade(response, 1, 3), "exactly one")
}
