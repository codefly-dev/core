package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/configuration"
	"github.com/codefly-dev/core/provider/manifest"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$`)

func Bytes(message proto.Message) ([]byte, error) {
	if message == nil || !message.ProtoReflect().IsValid() {
		return nil, fmt.Errorf("canonical protobuf message is required")
	}
	if err := rejectUnknownFields(message.ProtoReflect()); err != nil {
		return nil, err
	}
	if err := validatePublicValues(message.ProtoReflect()); err != nil {
		return nil, err
	}
	payload, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		return nil, err
	}
	name := string(message.ProtoReflect().Descriptor().FullName())
	out := make([]byte, 0, len(name)+1+len(payload))
	out = append(out, name...)
	out = append(out, '\n')
	out = append(out, payload...)
	return out, nil
}

func Digest(message proto.Message) (string, error) {
	data, err := Bytes(message)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

func BindingDesiredStateDigest(desired *providerv0.BindingDesiredState) (string, error) {
	if desired == nil {
		return "", fmt.Errorf("binding desired state is required")
	}
	clone := proto.Clone(desired).(*providerv0.BindingDesiredState)
	sort.Slice(clone.CredentialReferences, func(i, j int) bool {
		left, right := clone.CredentialReferences[i], clone.CredentialReferences[j]
		return fmt.Sprintf("%d\x00%s", left.GetPurpose(), left.GetReference()) < fmt.Sprintf("%d\x00%s", right.GetPurpose(), right.GetReference())
	})
	return Digest(clone)
}

func MaterialObservationDigest(observation *providerv0.MaterialObservation) (string, error) {
	if observation == nil {
		return "", fmt.Errorf("material observation is required")
	}
	if observation.GetComplete() && observation.GetNextCursor() != "" {
		return "", fmt.Errorf("complete material observation cannot carry next_cursor")
	}
	if !observation.GetComplete() && observation.GetNextCursor() == "" {
		return "", fmt.Errorf("incomplete material observation requires next_cursor")
	}
	clone := proto.Clone(observation).(*providerv0.MaterialObservation)
	sort.Slice(clone.Resources, func(i, j int) bool {
		return remoteIdentityKey(clone.Resources[i].GetIdentity()) < remoteIdentityKey(clone.Resources[j].GetIdentity())
	})
	return Digest(clone)
}

func ObserveResponseMaterialDigest(response *providerv0.ObserveResponse) (string, error) {
	if response == nil {
		return "", fmt.Errorf("observe response is required")
	}
	return MaterialObservationDigest(response.GetMaterial())
}

func OrderedPlanDigest(plan *providerv0.OrderedPlan) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("ordered plan is required")
	}
	clone := proto.Clone(plan).(*providerv0.OrderedPlan)
	clone.PlanDigest = ""
	seen := make(map[string]struct{}, len(clone.Actions))
	for index, action := range clone.Actions {
		if action == nil {
			return "", fmt.Errorf("ordered plan action %d is nil", index)
		}
		if err := ValidatePlanAction(action); err != nil {
			return "", fmt.Errorf("ordered plan action %d: %w", index, err)
		}
		if action.GetPosition() != uint32(index) {
			return "", fmt.Errorf("ordered plan action %q has position %d, want %d", action.GetActionId(), action.GetPosition(), index)
		}
		if action.GetActionId() == "" {
			return "", fmt.Errorf("ordered plan action %d has no action_id", index)
		}
		if _, duplicate := seen[action.GetActionId()]; duplicate {
			return "", fmt.Errorf("ordered plan action_id %q is duplicated", action.GetActionId())
		}
		seen[action.GetActionId()] = struct{}{}
	}
	return Digest(clone)
}

func ValidatePlanAction(action *providerv0.PlanAction) error {
	if action == nil {
		return fmt.Errorf("plan action is required")
	}
	if action.GetActionId() == "" || action.GetResourceType() == "" {
		return fmt.Errorf("plan action identity and resource type are required")
	}
	switch action.GetType() {
	case providerv0.ActionType_ACTION_TYPE_CREATE, providerv0.ActionType_ACTION_TYPE_REPLACE:
		if action.GetProspectiveRemoteId() == "" {
			return fmt.Errorf("create and replace actions require a prospective remote id")
		}
	case providerv0.ActionType_ACTION_TYPE_UPDATE, providerv0.ActionType_ACTION_TYPE_DELETE,
		providerv0.ActionType_ACTION_TYPE_IMPORT, providerv0.ActionType_ACTION_TYPE_MANUAL,
		providerv0.ActionType_ACTION_TYPE_BLOCKED, providerv0.ActionType_ACTION_TYPE_NO_OP,
		providerv0.ActionType_ACTION_TYPE_PROJECT_OUTPUT:
	default:
		return fmt.Errorf("unknown plan action type %d", action.GetType())
	}
	switch action.GetOwnership() {
	case providerv0.Ownership_OWNERSHIP_UNSPECIFIED, providerv0.Ownership_OWNERSHIP_OBSERVED,
		providerv0.Ownership_OWNERSHIP_OWNED, providerv0.Ownership_OWNERSHIP_ADOPTED,
		providerv0.Ownership_OWNERSHIP_UNMANAGED:
	default:
		return fmt.Errorf("unknown plan action ownership %d", action.GetOwnership())
	}
	return nil
}

func BindOrderedPlanDigest(plan *providerv0.OrderedPlan) (*providerv0.OrderedPlan, error) {
	if plan == nil {
		return nil, fmt.Errorf("ordered plan is required")
	}
	clone := proto.Clone(plan).(*providerv0.OrderedPlan)
	computed, err := OrderedPlanDigest(clone)
	if err != nil {
		return nil, err
	}
	if clone.GetPlanDigest() != "" && clone.GetPlanDigest() != computed {
		return nil, fmt.Errorf("ordered plan digest mismatch")
	}
	clone.PlanDigest = computed
	return clone, nil
}

func OutputTargetDigest(target *providerv0.OutputTarget) (string, error) {
	return Digest(target)
}

func OutputProposalDigest(proposal *providerv0.OutputProposal) (string, error) {
	if proposal == nil {
		return "", fmt.Errorf("output proposal is required")
	}
	clone := proto.Clone(proposal).(*providerv0.OutputProposal)
	clone.Digest = ""
	return Digest(clone)
}

func StateGenerationDigest(generation *providerv0.StateGeneration) (string, error) {
	return Digest(generation)
}

func AdmittedOriginDigest(origin *providerv0.AdmittedOrigin) (string, error) {
	if origin == nil {
		return "", fmt.Errorf("admitted origin is required")
	}
	clone := proto.Clone(origin).(*providerv0.AdmittedOrigin)
	clone.AdmissionDigest = ""
	return Digest(clone)
}

func PolicyApprovalInputDigest(input *providerv0.PolicyApprovalInput) (string, error) {
	return Digest(input)
}

func ManifestDigest(providerManifest *manifest.Manifest) (string, error) {
	return providerManifest.Digest()
}

func CatalogDigest(catalog *manifest.Catalog) (string, error) {
	return catalog.Digest()
}

func RequestDescriptorDigest(descriptor manifest.RequestDescriptor) (string, error) {
	return manifest.RequestDescriptorDigest(descriptor)
}

func rejectUnknownFields(message protoreflect.Message) error {
	if len(message.GetUnknown()) != 0 {
		return fmt.Errorf("canonical message %s contains unknown fields", message.Descriptor().FullName())
	}
	var found error
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsMap() {
			if field.MapValue().Kind() != protoreflect.MessageKind {
				return true
			}
			value.Map().Range(func(_ protoreflect.MapKey, entry protoreflect.Value) bool {
				found = rejectUnknownFields(entry.Message())
				return found == nil
			})
			return found == nil
		}
		if field.IsList() {
			if field.Kind() != protoreflect.MessageKind {
				return true
			}
			list := value.List()
			for i := 0; i < list.Len(); i++ {
				if found = rejectUnknownFields(list.Get(i).Message()); found != nil {
					return false
				}
			}
			return true
		}
		if field.Kind() == protoreflect.MessageKind {
			found = rejectUnknownFields(value.Message())
			return found == nil
		}
		return true
	})
	return found
}

func validatePublicValues(message protoreflect.Message) error {
	if message.Descriptor().FullName() == "codefly.services.provider.v0.PublicValue" {
		kind := message.WhichOneof(message.Descriptor().Oneofs().ByName("kind"))
		if kind == nil {
			return fmt.Errorf("public value kind is required")
		}
		switch kind.Name() {
		case "string_value":
			value := message.Get(kind).String()
			if configuration.LooksSecret(value) {
				return fmt.Errorf("public value contains a secret-shaped literal")
			}
		case "decimal_value":
			decimal := message.Get(kind).String()
			if decimal == "-0" || !decimalPattern.MatchString(decimal) {
				return fmt.Errorf("decimal value %q is not canonical", decimal)
			}
		case "null_value":
			if !message.Get(kind).Bool() {
				return fmt.Errorf("null_value must be true")
			}
		}
	}
	var found error
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsMap() {
			if field.MapValue().Kind() != protoreflect.MessageKind {
				return true
			}
			value.Map().Range(func(_ protoreflect.MapKey, entry protoreflect.Value) bool {
				found = validatePublicValues(entry.Message())
				return found == nil
			})
			return found == nil
		}
		if field.IsList() {
			if field.Kind() != protoreflect.MessageKind {
				return true
			}
			for i := 0; i < value.List().Len(); i++ {
				if found = validatePublicValues(value.List().Get(i).Message()); found != nil {
					return false
				}
			}
			return true
		}
		if field.Kind() != protoreflect.MessageKind {
			return true
		}
		nested := value.Message()
		found = validatePublicValues(nested)
		return found == nil
	})
	return found
}

func remoteIdentityKey(identity *providerv0.RemoteIdentity) string {
	if identity == nil {
		return ""
	}
	return identity.GetProvider() + "\x00" + identity.GetAccountId() + "\x00" + identity.GetResourceType() + "\x00" + identity.GetRemoteId()
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
