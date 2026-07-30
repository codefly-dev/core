package state_test

import (
	"strings"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/state"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestStateV1EncodeDecodeAndProspectiveIdentityRecovery(t *testing.T) {
	original := validState("binding-a")
	original.Checkpoint = &providerv0.ActionCheckpoint{
		CheckpointId:        "checkpoint-1",
		Delivery:            providerv0.DeliveryState_DELIVERY_STATE_SENT_OUTCOME_UNKNOWN,
		IdempotencyKey:      "binding-a/create/account",
		ProspectiveRemoteId: "prospective-account-123",
	}
	encoded, err := state.Encode(original)
	require.NoError(t, err)
	decoded, err := state.Decode(encoded, 1)
	require.NoError(t, err)
	require.True(t, proto.Equal(original, decoded))
	require.Equal(t, "prospective-account-123", decoded.GetCheckpoint().GetProspectiveRemoteId())
}

func TestStateV1FailsClosedOnNewerOrUnknownState(t *testing.T) {
	newer := validState("binding-a")
	newer.StateSchemaVersion = 2
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(newer)
	require.NoError(t, err)
	_, err = state.Decode(encoded, 1)
	require.ErrorContains(t, err, "newer than supported")

	validEncoded, err := state.Encode(validState("binding-a"))
	require.NoError(t, err)
	withUnknown := append(validEncoded, 0xa0, 0x06, 0x01)
	_, err = state.Decode(withUnknown, 1)
	require.ErrorContains(t, err, "unknown fields")

	unknownOwnership := validState("binding-a")
	unknownOwnership.Ownership = providerv0.Ownership(99)
	_, err = state.Encode(unknownOwnership)
	require.ErrorContains(t, err, "ownership")

	unknownPurpose := validState("binding-a")
	unknownPurpose.SecretReferences[0].Purpose = providerv0.CredentialPurpose(99)
	_, err = state.Encode(unknownPurpose)
	require.ErrorContains(t, err, "unknown purpose")
}

func TestWorkspaceOwnershipIndexRejectsDuplicateRemoteOwnership(t *testing.T) {
	first := validState("binding-a")
	second := validState("binding-b")
	_, err := state.NewOwnershipIndex(first, second)
	require.ErrorContains(t, err, "already owned")

	second.Binding.WorkspaceId = "other-workspace"
	index, err := state.NewOwnershipIndex(first, second)
	require.NoError(t, err)
	owner, ok := index.Owner("workspace", first.RemoteIdentity)
	require.True(t, ok)
	require.Equal(t, "environment/binding-a", owner)
}

func TestStateSchemaCannotRepresentRawSecretsOrBodies(t *testing.T) {
	descriptor := (&providerv0.ProviderStateV1{}).ProtoReflect().Descriptor()
	for i := 0; i < descriptor.Fields().Len(); i++ {
		field := descriptor.Fields().Get(i)
		name := string(field.Name())
		require.NotContains(t, name, "raw")
		require.NotEqual(t, "body", name)
		require.False(t, strings.HasSuffix(name, "secret_value"))
		require.NotEqual(t, "bytes", field.Kind().String())
	}

	providerState := validState("binding-a")
	providerState.SafeObservedFields = map[string]*providerv0.PublicValue{
		"api_token": {Kind: &providerv0.PublicValue_StringValue{StringValue: "sk_live_1234567890abcdef"}},
	}
	_, err := state.Encode(providerState)
	require.ErrorContains(t, err, "secret-shaped")
}

func validState(bindingID string) *providerv0.ProviderStateV1 {
	digest := "sha256:" + strings.Repeat("1", 64)
	return &providerv0.ProviderStateV1{
		StateSchemaVersion:    1,
		Generation:            1,
		Binding:               &providerv0.BindingAddress{WorkspaceId: "workspace", EnvironmentId: "environment", BindingId: bindingID},
		ProviderId:            "codefly.dev/fixture",
		ProviderVersion:       "1.2.3",
		ManifestSchemaVersion: "codefly.provider-manifest/v0",
		ManifestDigest:        digest,
		ArtifactDigest:        digest,
		RemoteIdentity: &providerv0.RemoteIdentity{
			Provider: "fixture", AccountId: "account", ResourceType: "account", RemoteId: "remote-123",
		},
		Ownership:      providerv0.Ownership_OWNERSHIP_OWNED,
		OwnershipStamp: "host-stamp",
		SecretReferences: []*providerv0.OpaqueReference{{
			Reference: "secret://runtime", Purpose: providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
			SafeFingerprint: digest,
		}},
	}
}
