package state

import (
	"fmt"
	"regexp"
	"strings"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/canonical"
	"github.com/codefly-dev/core/provider/configuration"
	"github.com/codefly-dev/core/provider/manifest"
	"google.golang.org/protobuf/proto"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type OwnershipIndex struct {
	owners map[string]string
}

func Encode(providerState *providerv0.ProviderStateV1) ([]byte, error) {
	if err := Validate(providerState, manifest.StateSchemaVersionV1); err != nil {
		return nil, err
	}
	if _, err := canonical.Bytes(providerState); err != nil {
		return nil, err
	}
	return (proto.MarshalOptions{Deterministic: true}).Marshal(providerState)
}

func Decode(data []byte, maxSupportedVersion uint32) (*providerv0.ProviderStateV1, error) {
	if maxSupportedVersion == 0 {
		return nil, fmt.Errorf("maximum supported state version is required")
	}
	var providerState providerv0.ProviderStateV1
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(data, &providerState); err != nil {
		return nil, fmt.Errorf("decode provider state: %w", err)
	}
	if err := Validate(&providerState, maxSupportedVersion); err != nil {
		return nil, err
	}
	if _, err := canonical.Digest(&providerState); err != nil {
		return nil, fmt.Errorf("decode provider state: %w", err)
	}
	return &providerState, nil
}

func Validate(providerState *providerv0.ProviderStateV1, maxSupportedVersion uint32) error {
	if providerState == nil {
		return fmt.Errorf("provider state is required")
	}
	if providerState.GetStateSchemaVersion() == 0 {
		return fmt.Errorf("provider state schema version is required")
	}
	if providerState.GetStateSchemaVersion() > maxSupportedVersion {
		return fmt.Errorf("provider state version %d is newer than supported version %d", providerState.GetStateSchemaVersion(), maxSupportedVersion)
	}
	if providerState.GetStateSchemaVersion() != manifest.StateSchemaVersionV1 {
		return fmt.Errorf("unsupported provider state version %d", providerState.GetStateSchemaVersion())
	}
	binding := providerState.GetBinding()
	if binding.GetWorkspaceId() == "" || binding.GetEnvironmentId() == "" || binding.GetBindingId() == "" {
		return fmt.Errorf("provider state binding address is incomplete")
	}
	if providerState.GetProviderId() == "" || providerState.GetProviderVersion() == "" || providerState.GetManifestSchemaVersion() == "" {
		return fmt.Errorf("provider state provider and schema identity are required")
	}
	if !digestPattern.MatchString(providerState.GetManifestDigest()) || !digestPattern.MatchString(providerState.GetArtifactDigest()) {
		return fmt.Errorf("provider state manifest and artifact digests must be canonical sha256 values")
	}
	switch providerState.GetOwnership() {
	case providerv0.Ownership_OWNERSHIP_OBSERVED, providerv0.Ownership_OWNERSHIP_OWNED,
		providerv0.Ownership_OWNERSHIP_ADOPTED, providerv0.Ownership_OWNERSHIP_UNMANAGED:
	default:
		return fmt.Errorf("provider state ownership is required")
	}
	if providerState.GetOwnership() == providerv0.Ownership_OWNERSHIP_OWNED ||
		providerState.GetOwnership() == providerv0.Ownership_OWNERSHIP_ADOPTED {
		identity := providerState.GetRemoteIdentity()
		if identity.GetProvider() == "" || identity.GetAccountId() == "" || identity.GetResourceType() == "" || identity.GetRemoteId() == "" {
			return fmt.Errorf("owned or adopted provider state requires exact remote identity")
		}
		if providerState.GetOwnershipStamp() == "" {
			return fmt.Errorf("owned or adopted provider state requires ownership stamp")
		}
	}
	if checkpoint := providerState.GetCheckpoint(); checkpoint != nil {
		switch checkpoint.GetDelivery() {
		case providerv0.DeliveryState_DELIVERY_STATE_NOT_SENT,
			providerv0.DeliveryState_DELIVERY_STATE_SENT_OUTCOME_UNKNOWN,
			providerv0.DeliveryState_DELIVERY_STATE_RESPONSE_RECEIVED:
		default:
			return fmt.Errorf("provider state checkpoint delivery is unknown")
		}
		if checkpoint.GetDelivery() != providerv0.DeliveryState_DELIVERY_STATE_NOT_SENT && checkpoint.GetIdempotencyKey() == "" {
			return fmt.Errorf("sent provider state checkpoint requires idempotency key")
		}
	}
	if receipt := providerState.GetReceipt(); receipt != nil {
		if receipt.GetArtifactDigest() != providerState.GetArtifactDigest() {
			return fmt.Errorf("provider state receipt artifact digest mismatch")
		}
		switch receipt.GetCertainty() {
		case providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE,
			providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_PARTIAL,
			providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_UNCERTAIN:
		default:
			return fmt.Errorf("provider state receipt certainty is unknown")
		}
		if err := canonical.ValidatePlanAction(receipt.GetAction()); err != nil {
			return fmt.Errorf("provider state receipt: %w", err)
		}
	}
	for i, reference := range providerState.GetSecretReferences() {
		if reference.GetReference() == "" || reference.GetSafeFingerprint() == "" {
			return fmt.Errorf("provider state secret_references[%d] is incomplete", i)
		}
		switch reference.GetPurpose() {
		case providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT,
			providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
			providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_BUILD,
			providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_WEBHOOK_VERIFICATION:
		default:
			return fmt.Errorf("provider state secret_references[%d] has unknown purpose", i)
		}
	}
	for name, values := range map[string]map[string]*providerv0.PublicValue{
		"safe_observed_fields":  providerState.GetSafeObservedFields(),
		"provider_owned_fields": providerState.GetProviderOwnedFields(),
		"recovery_data":         providerState.GetRecoveryData(),
	} {
		if err := validateSafeValues(name, values); err != nil {
			return err
		}
	}
	for i, record := range providerState.GetUpgradeHistory() {
		if err := ValidateUpgradeRecord(record); err != nil {
			return fmt.Errorf("provider state upgrade_history[%d]: %w", i, err)
		}
	}
	return nil
}

func validateSafeValues(field string, values map[string]*providerv0.PublicValue) error {
	for key, value := range values {
		normalized := strings.ToUpper(key)
		for _, marker := range []string{"PASSWORD", "SECRET", "TOKEN", "PRIVATE_KEY", "CREDENTIAL"} {
			if strings.Contains(normalized, marker) {
				return fmt.Errorf("provider state %s key %q is secret-shaped", field, key)
			}
		}
		if err := validateSafeValue(field+"."+key, value); err != nil {
			return err
		}
	}
	return nil
}

func validateSafeValue(field string, value *providerv0.PublicValue) error {
	if value == nil {
		return fmt.Errorf("provider state %s is nil", field)
	}
	switch kind := value.Kind.(type) {
	case *providerv0.PublicValue_StringValue:
		if configuration.LooksSecret(kind.StringValue) {
			return fmt.Errorf("provider state %s contains a secret-shaped value", field)
		}
	case *providerv0.PublicValue_ListValue:
		for index, entry := range kind.ListValue.GetValues() {
			if err := validateSafeValue(fmt.Sprintf("%s[%d]", field, index), entry); err != nil {
				return err
			}
		}
	case *providerv0.PublicValue_ObjectValue:
		if err := validateSafeValues(field, kind.ObjectValue.GetFields()); err != nil {
			return err
		}
	case *providerv0.PublicValue_BoolValue, *providerv0.PublicValue_IntegerValue,
		*providerv0.PublicValue_DecimalValue, *providerv0.PublicValue_NullValue:
	default:
		return fmt.Errorf("provider state %s has no public value kind", field)
	}
	return nil
}

func ValidateUpgradeRecord(record *providerv0.UpgradeRecord) error {
	if record == nil {
		return fmt.Errorf("upgrade record is required")
	}
	if record.GetToVersion() != record.GetFromVersion()+1 {
		return fmt.Errorf("state upgrades must advance exactly one version")
	}
	if record.GetAgentVersion() == "" || !digestPattern.MatchString(record.GetArtifactDigest()) ||
		!digestPattern.MatchString(record.GetPriorDigest()) || !digestPattern.MatchString(record.GetResultDigest()) {
		return fmt.Errorf("upgrade record identity and digests are incomplete")
	}
	if record.GetUpgradedAt() == nil || record.GetUpgradedAt().CheckValid() != nil {
		return fmt.Errorf("upgrade record timestamp is invalid")
	}
	return nil
}

func NewOwnershipIndex(states ...*providerv0.ProviderStateV1) (*OwnershipIndex, error) {
	index := &OwnershipIndex{owners: make(map[string]string)}
	for _, providerState := range states {
		if err := index.Add(providerState); err != nil {
			return nil, err
		}
	}
	return index, nil
}

func (i *OwnershipIndex) Add(providerState *providerv0.ProviderStateV1) error {
	if i == nil {
		return fmt.Errorf("ownership index is required")
	}
	if err := Validate(providerState, manifest.StateSchemaVersionV1); err != nil {
		return err
	}
	if providerState.GetOwnership() != providerv0.Ownership_OWNERSHIP_OWNED &&
		providerState.GetOwnership() != providerv0.Ownership_OWNERSHIP_ADOPTED {
		return nil
	}
	if i.owners == nil {
		i.owners = make(map[string]string)
	}
	key := remoteOwnershipKey(providerState.GetBinding().GetWorkspaceId(), providerState.GetRemoteIdentity())
	binding := bindingKey(providerState.GetBinding())
	if owner, exists := i.owners[key]; exists && owner != binding {
		return fmt.Errorf("remote identity %q is already owned by binding %q in workspace", remoteIdentityKey(providerState.GetRemoteIdentity()), owner)
	}
	i.owners[key] = binding
	return nil
}

func (i *OwnershipIndex) Owner(workspaceID string, identity *providerv0.RemoteIdentity) (string, bool) {
	if i == nil {
		return "", false
	}
	owner, ok := i.owners[remoteOwnershipKey(workspaceID, identity)]
	return owner, ok
}

func remoteOwnershipKey(workspace string, identity *providerv0.RemoteIdentity) string {
	return workspace + "\x00" + remoteIdentityKey(identity)
}

func remoteIdentityKey(identity *providerv0.RemoteIdentity) string {
	if identity == nil {
		return ""
	}
	return identity.GetProvider() + "\x00" + identity.GetAccountId() + "\x00" + identity.GetResourceType() + "\x00" + identity.GetRemoteId()
}

func bindingKey(binding *providerv0.BindingAddress) string {
	if binding == nil {
		return ""
	}
	return binding.GetEnvironmentId() + "/" + binding.GetBindingId()
}
