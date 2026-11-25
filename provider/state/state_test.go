package state_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	providerv1 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v1"
	"github.com/codefly-dev/core/provider/state"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestOwnershipIndexIsSafeForConcurrentAddAndOwner(t *testing.T) {
	index, err := state.NewOwnershipIndex()
	require.NoError(t, err)
	var wg sync.WaitGroup
	for n := range 16 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			providerState := validState(fmt.Sprintf("binding-%d", n))
			providerState.Binding.WorkspaceId = fmt.Sprintf("workspace-%d", n)
			require.NoError(t, index.Add(providerState))
			index.Owner(fmt.Sprintf("workspace-%d", n), providerState.RemoteIdentity)
		}(n)
	}
	wg.Wait()
}

func TestStateV1EncodeDecodeAndProspectiveIdentityRecovery(t *testing.T) {
	original := validState("binding-a")
	original.Checkpoint = &providerv1.ActionCheckpoint{
		CheckpointId:        "checkpoint-1",
		Delivery:            providerv1.DeliveryState_DELIVERY_STATE_SENT_OUTCOME_UNKNOWN,
		IdempotencyKey:      "binding-a/create/account",
		ProspectiveRemoteId: "prospective-account-123",
	}
	encoded, err := state.Encode(state.WrapV1(original))
	require.NoError(t, err)
	decoded, err := state.Decode(encoded, 1)
	require.NoError(t, err)
	require.True(t, proto.Equal(original, decoded.GetV1()))
	require.Equal(t, "prospective-account-123", decoded.GetV1().GetCheckpoint().GetProspectiveRemoteId())
}

func TestStateV1FailsClosedOnNewerOrUnknownState(t *testing.T) {
	newer := &providerv1.ProviderState{SchemaVersion: 2}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(newer)
	require.NoError(t, err)
	_, err = state.Decode(encoded, 1)
	require.ErrorContains(t, err, "newer than supported")

	validEncoded, err := state.Encode(state.WrapV1(validState("binding-a")))
	require.NoError(t, err)
	withUnknown := append(validEncoded, 0xa0, 0x06, 0x01)
	_, err = state.Decode(withUnknown, 1)
	require.ErrorContains(t, err, "unknown fields")

	unknownOwnership := validState("binding-a")
	unknownOwnership.Ownership = providerv1.Ownership(99)
	_, err = state.Encode(state.WrapV1(unknownOwnership))
	require.ErrorContains(t, err, "ownership")

	unknownPurpose := validState("binding-a")
	unknownPurpose.SecretReferences[0].Purpose = providerv1.CredentialPurpose(99)
	_, err = state.Encode(state.WrapV1(unknownPurpose))
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
	require.Equal(t, "environment", owner.EnvironmentId)
	require.Equal(t, "binding-a", owner.BindingId)
}

func TestWorkspaceOwnershipIndexUsesUnambiguousComponentKeys(t *testing.T) {
	first := validState("c")
	first.Binding.EnvironmentId = "a/b"
	second := validState("b/c")
	second.Binding.EnvironmentId = "a"
	_, err := state.NewOwnershipIndex(first, second)
	require.ErrorContains(t, err, "already owned")

	first = validState("binding-a")
	first.RemoteIdentity.Provider = "provider"
	first.RemoteIdentity.AccountId = "account\x00region"
	second = validState("binding-b")
	second.RemoteIdentity.Provider = "provider\x00account"
	second.RemoteIdentity.AccountId = "region"
	_, err = state.NewOwnershipIndex(first, second)
	require.NoError(t, err)
}

func TestStateSchemaCannotRepresentRawSecretsOrBodies(t *testing.T) {
	descriptor := (&providerv1.ProviderStateV1{}).ProtoReflect().Descriptor()
	for i := 0; i < descriptor.Fields().Len(); i++ {
		field := descriptor.Fields().Get(i)
		name := string(field.Name())
		require.NotContains(t, name, "raw")
		require.NotEqual(t, "body", name)
		require.False(t, strings.HasSuffix(name, "secret_value"))
		require.NotEqual(t, "bytes", field.Kind().String())
	}

	providerState := validState("binding-a")
	providerState.SafeObservedFields = map[string]*providerv1.PublicValue{
		"api_token": {Kind: &providerv1.PublicValue_StringValue{StringValue: "sk_live_1234567890abcdef"}},
	}
	_, err := state.Encode(state.WrapV1(providerState))
	require.ErrorContains(t, err, "secret-shaped")

	providerState = validState("binding-a")
	providerState.SecretReferences[0].Reference = "sk_live_1234567890abcdef"
	_, err = state.Encode(state.WrapV1(providerState))
	require.ErrorContains(t, err, "opaque reference")

	providerState = validState("binding-a")
	providerState.SecretReferences[0].SafeFingerprint = "raw-fingerprint"
	_, err = state.Encode(state.WrapV1(providerState))
	require.ErrorContains(t, err, "canonical sha256")
}

func TestStateReceiptRejectsUnknownDeliveryState(t *testing.T) {
	providerState := validState("binding-a")
	providerState.Receipt = &providerv1.ActionReceipt{
		ArtifactDigest: providerState.ArtifactDigest,
		Delivery:       providerv1.DeliveryState(99),
		Certainty:      providerv1.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE,
		Action: &providerv1.PlanAction{
			ActionId: "noop", Type: providerv1.ActionType_ACTION_TYPE_NO_OP, ResourceType: "account",
		},
	}
	_, err := state.Encode(state.WrapV1(providerState))
	require.ErrorContains(t, err, "receipt delivery is unknown")
}

func TestStateRejectsMalformedObservationPlanAndOutputDigests(t *testing.T) {
	// Absent is allowed: these fields are unset until a first observation/plan/output.
	_, err := state.Encode(state.WrapV1(validState("binding-a")))
	require.NoError(t, err)

	valid := "sha256:" + strings.Repeat("1", 64)
	for _, mutate := range []struct {
		name  string
		apply func(*providerv1.ProviderStateV1)
	}{
		{"material_observation_digest", func(s *providerv1.ProviderStateV1) { s.MaterialObservationDigest = "not-a-digest" }},
		{"plan_digest", func(s *providerv1.ProviderStateV1) { s.PlanDigest = "sha256:short" }},
		{"output_digest", func(s *providerv1.ProviderStateV1) { s.OutputDigest = "md5:" + strings.Repeat("1", 64) }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			providerState := validState("binding-a")
			mutate.apply(providerState)
			_, err := state.Encode(state.WrapV1(providerState))
			require.ErrorContains(t, err, mutate.name+" must be a canonical sha256 value")

			providerState = validState("binding-a")
			providerState.MaterialObservationDigest = valid
			providerState.PlanDigest = valid
			providerState.OutputDigest = valid
			_, err = state.Encode(state.WrapV1(providerState))
			require.NoError(t, err)
		})
	}
}

func validState(bindingID string) *providerv1.ProviderStateV1 {
	digest := "sha256:" + strings.Repeat("1", 64)
	return &providerv1.ProviderStateV1{
		StateSchemaVersion:    1,
		Generation:            1,
		Binding:               &providerv1.BindingAddress{WorkspaceId: "workspace", EnvironmentId: "environment", BindingId: bindingID},
		ProviderId:            "codefly.dev/fixture",
		ProviderVersion:       "1.2.3",
		ManifestSchemaVersion: "codefly.provider-manifest/v0",
		ManifestDigest:        digest,
		ArtifactDigest:        digest,
		RemoteIdentity: &providerv1.RemoteIdentity{
			Provider: "fixture", AccountId: "account", ResourceType: "account", RemoteId: "remote-123",
		},
		Ownership:      providerv1.Ownership_OWNERSHIP_OWNED,
		OwnershipStamp: "host-stamp",
		SecretReferences: []*providerv1.OpaqueReference{{
			Reference: "secret://runtime", Purpose: providerv1.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
			SafeFingerprint: digest,
		}},
	}
}
