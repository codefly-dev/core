package state

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/canonical"
	"github.com/codefly-dev/core/provider/configuration"
	"github.com/codefly-dev/core/provider/manifest"
	"google.golang.org/protobuf/proto"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type OwnershipIndex struct {
	owners map[string]ownershipRecord
}

type ownershipRecord struct {
	bindingKey string
	binding    *providerv0.BindingAddress
}

func WrapV1(providerState *providerv0.ProviderStateV1) *providerv0.ProviderState {
	if providerState == nil {
		return nil
	}
	return &providerv0.ProviderState{
		SchemaVersion: manifest.StateSchemaVersionV1,
		Schema: &providerv0.ProviderState_V1{
			V1: proto.Clone(providerState).(*providerv0.ProviderStateV1),
		},
	}
}

func Encode(providerState *providerv0.ProviderState) ([]byte, error) {
	if err := ValidateVersioned(providerState, manifest.StateSchemaVersionV1); err != nil {
		return nil, err
	}
	if _, err := canonical.Bytes(providerState); err != nil {
		return nil, err
	}
	return (proto.MarshalOptions{Deterministic: true}).Marshal(providerState)
}

func Decode(data []byte, maxSupportedVersion uint32) (*providerv0.ProviderState, error) {
	if maxSupportedVersion == 0 {
		return nil, fmt.Errorf("maximum supported state version is required")
	}
	var providerState providerv0.ProviderState
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(data, &providerState); err != nil {
		return nil, fmt.Errorf("decode provider state: %w", err)
	}
	if err := ValidateVersioned(&providerState, maxSupportedVersion); err != nil {
		return nil, err
	}
	if _, err := canonical.Digest(&providerState); err != nil {
		return nil, fmt.Errorf("decode provider state: %w", err)
	}
	return &providerState, nil
}

func ValidateVersioned(providerState *providerv0.ProviderState, maxSupportedVersion uint32) error {
	if providerState == nil {
		return fmt.Errorf("provider state is required")
	}
	if providerState.GetSchemaVersion() == 0 {
		return fmt.Errorf("provider state schema version is required")
	}
	if providerState.GetSchemaVersion() > maxSupportedVersion {
		return fmt.Errorf("provider state version %d is newer than supported version %d", providerState.GetSchemaVersion(), maxSupportedVersion)
	}
	switch schema := providerState.GetSchema().(type) {
	case *providerv0.ProviderState_V1:
		if providerState.GetSchemaVersion() != manifest.StateSchemaVersionV1 {
			return fmt.Errorf("provider state schema version %d does not match v1 payload", providerState.GetSchemaVersion())
		}
		return Validate(schema.V1, maxSupportedVersion)
	default:
		return fmt.Errorf("unsupported provider state version %d", providerState.GetSchemaVersion())
	}
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
		switch receipt.GetDelivery() {
		case providerv0.DeliveryState_DELIVERY_STATE_NOT_SENT,
			providerv0.DeliveryState_DELIVERY_STATE_SENT_OUTCOME_UNKNOWN,
			providerv0.DeliveryState_DELIVERY_STATE_RESPONSE_RECEIVED:
		default:
			return fmt.Errorf("provider state receipt delivery is unknown")
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
		for i, reference := range receipt.GetCaptureReferences() {
			if err := validateOpaqueReference(reference, false); err != nil {
				return fmt.Errorf("provider state receipt capture_references[%d]: %w", i, err)
			}
		}
	}
	for i, reference := range providerState.GetSecretReferences() {
		if err := validateOpaqueReference(reference, true); err != nil {
			return fmt.Errorf("provider state secret_references[%d]: %w", i, err)
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

func validateOpaqueReference(reference *providerv0.OpaqueReference, requireFingerprint bool) error {
	if reference == nil {
		return fmt.Errorf("opaque reference is required")
	}
	if err := configuration.ValidateOpaqueReference(reference.GetReference()); err != nil {
		return err
	}
	switch reference.GetPurpose() {
	case providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT,
		providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
		providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_BUILD,
		providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_WEBHOOK_VERIFICATION:
	default:
		return fmt.Errorf("opaque reference has unknown purpose")
	}
	if requireFingerprint && !digestPattern.MatchString(reference.GetSafeFingerprint()) {
		return fmt.Errorf("opaque reference safe fingerprint must be a canonical sha256 value")
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
	index := &OwnershipIndex{owners: make(map[string]ownershipRecord)}
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
		i.owners = make(map[string]ownershipRecord)
	}
	key := remoteOwnershipKey(providerState.GetBinding().GetWorkspaceId(), providerState.GetRemoteIdentity())
	binding := proto.Clone(providerState.GetBinding()).(*providerv0.BindingAddress)
	bindingIdentity := bindingKey(binding)
	if owner, exists := i.owners[key]; exists && owner.bindingKey != bindingIdentity {
		return fmt.Errorf("remote identity is already owned by binding %q in workspace", bindingLabel(owner.binding))
	}
	i.owners[key] = ownershipRecord{bindingKey: bindingIdentity, binding: binding}
	return nil
}

func (i *OwnershipIndex) Owner(workspaceID string, identity *providerv0.RemoteIdentity) (*providerv0.BindingAddress, bool) {
	if i == nil {
		return nil, false
	}
	owner, ok := i.owners[remoteOwnershipKey(workspaceID, identity)]
	if !ok {
		return nil, false
	}
	return proto.Clone(owner.binding).(*providerv0.BindingAddress), true
}

func remoteOwnershipKey(workspace string, identity *providerv0.RemoteIdentity) string {
	if identity == nil {
		return tupleKey(workspace)
	}
	return tupleKey(workspace, identity.GetProvider(), identity.GetAccountId(), identity.GetResourceType(), identity.GetRemoteId())
}

func remoteIdentityKey(identity *providerv0.RemoteIdentity) string {
	if identity == nil {
		return ""
	}
	return tupleKey(identity.GetProvider(), identity.GetAccountId(), identity.GetResourceType(), identity.GetRemoteId())
}

func bindingKey(binding *providerv0.BindingAddress) string {
	if binding == nil {
		return ""
	}
	return tupleKey(binding.GetEnvironmentId(), binding.GetBindingId())
}

func bindingLabel(binding *providerv0.BindingAddress) string {
	if binding == nil {
		return ""
	}
	return binding.GetEnvironmentId() + "/" + binding.GetBindingId()
}

func tupleKey(parts ...string) string {
	var key strings.Builder
	for _, part := range parts {
		key.WriteString(strconv.Itoa(len(part)))
		key.WriteByte(':')
		key.WriteString(part)
	}
	return key.String()
}
