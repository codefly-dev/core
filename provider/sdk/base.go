package sdk

import (
	"context"
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/canonical"
	"github.com/codefly-dev/core/provider/configuration"
	providerstate "github.com/codefly-dev/core/provider/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Base struct {
	providerv0.UnimplementedProviderServer
	information *providerv0.GetProviderInformationResponse
}

func NewBase(information *providerv0.GetProviderInformationResponse) (*Base, error) {
	if information == nil || information.GetArtifact() == nil || information.GetCatalog() == nil {
		return nil, fmt.Errorf("provider information requires artifact and runtime catalog")
	}
	if information.GetArtifact().GetArtifactDigest() == "" || information.GetArtifact().GetManifestDigest() == "" ||
		information.GetCatalog().GetDigest() == "" {
		return nil, fmt.Errorf("provider information digests are required")
	}
	return &Base{information: proto.Clone(information).(*providerv0.GetProviderInformationResponse)}, nil
}

func (b *Base) GetProviderInformation(_ context.Context, request *providerv0.GetProviderInformationRequest) (*providerv0.GetProviderInformationResponse, error) {
	if b == nil || b.information == nil {
		return nil, status.Error(codes.FailedPrecondition, "provider information is not configured")
	}
	if request == nil || !proto.Equal(request.GetArtifact(), b.information.GetArtifact()) {
		return nil, status.Error(codes.FailedPrecondition, "provider artifact identity mismatch")
	}
	return proto.Clone(b.information).(*providerv0.GetProviderInformationResponse), nil
}

func Diagnostic(severity basev0.FailureDiagnostic_Severity, namespace, code, message string) (*basev0.FailureDiagnostic, error) {
	if severity == basev0.FailureDiagnostic_SEVERITY_UNSPECIFIED {
		return nil, fmt.Errorf("diagnostic severity is required")
	}
	if namespace == "" || !strings.HasSuffix(namespace, ".") || code == "" || strings.Contains(code, ".") {
		return nil, fmt.Errorf("diagnostic namespace and local code are invalid")
	}
	if len(message) == 0 || len(message) > 2048 {
		return nil, fmt.Errorf("diagnostic message must contain 1 to 2048 bytes")
	}
	return &basev0.FailureDiagnostic{Severity: severity, Code: namespace + code, Message: message}, nil
}

func Observation(material *providerv0.MaterialObservation, volatile *providerv0.VolatileObservation) (*providerv0.ObserveResponse, error) {
	if _, err := canonical.MaterialObservationDigest(material); err != nil {
		return nil, err
	}
	response := &providerv0.ObserveResponse{Material: proto.Clone(material).(*providerv0.MaterialObservation)}
	if volatile != nil {
		response.Volatile = proto.Clone(volatile).(*providerv0.VolatileObservation)
	}
	return response, nil
}

func Plan(plan *providerv0.OrderedPlan) (*providerv0.OrderedPlan, error) {
	return canonical.BindOrderedPlanDigest(plan)
}

func PlannedRequest(request *providerv0.PlannedRequest) (*providerv0.PlannedRequest, error) {
	return canonical.BindPlannedRequestDigest(request)
}

func NewCreateAction(id string, position uint32, resourceType, prospectiveRemoteID string) (*providerv0.PlanAction, error) {
	return newAction(id, position, providerv0.ActionType_ACTION_TYPE_CREATE, resourceType, prospectiveRemoteID)
}

func NewUpdateAction(id string, position uint32, resourceType string) (*providerv0.PlanAction, error) {
	return newAction(id, position, providerv0.ActionType_ACTION_TYPE_UPDATE, resourceType, "")
}

func NewReplaceAction(id string, position uint32, resourceType, prospectiveRemoteID string) (*providerv0.PlanAction, error) {
	return newAction(id, position, providerv0.ActionType_ACTION_TYPE_REPLACE, resourceType, prospectiveRemoteID)
}

func NewDeleteAction(id string, position uint32, resourceType string) (*providerv0.PlanAction, error) {
	return newAction(id, position, providerv0.ActionType_ACTION_TYPE_DELETE, resourceType, "")
}

func NewImportAction(id string, position uint32, resourceType string) (*providerv0.PlanAction, error) {
	return newAction(id, position, providerv0.ActionType_ACTION_TYPE_IMPORT, resourceType, "")
}

func NewManualAction(id string, position uint32, resourceType string) (*providerv0.PlanAction, error) {
	return newAction(id, position, providerv0.ActionType_ACTION_TYPE_MANUAL, resourceType, "")
}

func NewBlockedAction(id string, position uint32, resourceType string) (*providerv0.PlanAction, error) {
	return newAction(id, position, providerv0.ActionType_ACTION_TYPE_BLOCKED, resourceType, "")
}

func NewNoOpAction(id string, position uint32, resourceType string) (*providerv0.PlanAction, error) {
	return newAction(id, position, providerv0.ActionType_ACTION_TYPE_NO_OP, resourceType, "")
}

func NewProjectOutputAction(id string, position uint32) (*providerv0.PlanAction, error) {
	return newAction(id, position, providerv0.ActionType_ACTION_TYPE_PROJECT_OUTPUT, "project-output", "")
}

func BindOutputProposal(proposal *providerv0.OutputProposal) (*providerv0.OutputProposal, error) {
	if proposal == nil || proposal.GetContract() == "" || proposal.GetTargetGeneration() == 0 {
		return nil, fmt.Errorf("output proposal contract and target generation are required")
	}
	clone := proto.Clone(proposal).(*providerv0.OutputProposal)
	computed, err := canonical.OutputProposalDigest(clone)
	if err != nil {
		return nil, err
	}
	if clone.GetDigest() != "" && clone.GetDigest() != computed {
		return nil, fmt.Errorf("output proposal digest mismatch")
	}
	clone.Digest = computed
	return clone, nil
}

func DecodeFilteredResponse(response *providerv0.ExecuteRequestResponse) (map[string]*providerv0.PublicValue, error) {
	if response == nil {
		return nil, fmt.Errorf("broker response is required")
	}
	switch response.GetDelivery() {
	case providerv0.DeliveryState_DELIVERY_STATE_NOT_SENT,
		providerv0.DeliveryState_DELIVERY_STATE_SENT_OUTCOME_UNKNOWN,
		providerv0.DeliveryState_DELIVERY_STATE_RESPONSE_RECEIVED:
	default:
		return nil, fmt.Errorf("broker response delivery is unknown")
	}
	switch response.GetCertainty() {
	case providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE,
		providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_PARTIAL,
		providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_UNCERTAIN:
	default:
		return nil, fmt.Errorf("broker response certainty is unknown")
	}
	fields := make(map[string]*providerv0.PublicValue, len(response.GetForwarded()))
	for i, field := range response.GetForwarded() {
		if field.GetSelector() == "" || field.GetValue() == nil {
			return nil, fmt.Errorf("broker response forwarded[%d] is incomplete", i)
		}
		if _, duplicate := fields[field.GetSelector()]; duplicate {
			return nil, fmt.Errorf("broker response selector %q is duplicated", field.GetSelector())
		}
		if _, err := canonical.Digest(field.GetValue()); err != nil {
			return nil, fmt.Errorf("broker response forwarded[%d]: %w", i, err)
		}
		fields[field.GetSelector()] = proto.Clone(field.GetValue()).(*providerv0.PublicValue)
	}
	return fields, nil
}

func HandleCaptureResult(result *providerv0.CaptureResult) (*providerv0.OpaqueReference, error) {
	if result == nil || result.GetCaptureId() == "" {
		return nil, fmt.Errorf("capture result identity is required")
	}
	if !result.GetCaptured() {
		return nil, fmt.Errorf("capture %q is not durable", result.GetCaptureId())
	}
	reference := result.GetSinkReference()
	if reference == nil {
		return nil, fmt.Errorf("capture %q has no opaque sink reference", result.GetCaptureId())
	}
	if err := configuration.ValidateOpaqueReference(reference.GetReference()); err != nil {
		return nil, fmt.Errorf("capture %q: %w", result.GetCaptureId(), err)
	}
	switch reference.GetPurpose() {
	case providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT,
		providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
		providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_BUILD,
		providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_WEBHOOK_VERIFICATION:
	default:
		return nil, fmt.Errorf("capture %q has unknown credential purpose", result.GetCaptureId())
	}
	return proto.Clone(reference).(*providerv0.OpaqueReference), nil
}

func ValidateUpgrade(response *providerv0.UpgradeStateResponse, fromVersion, toVersion uint32) error {
	if response == nil || response.GetState() == nil || response.GetRecord() == nil {
		return fmt.Errorf("state upgrade response is incomplete")
	}
	if toVersion != fromVersion+1 || response.GetRecord().GetFromVersion() != fromVersion ||
		response.GetRecord().GetToVersion() != toVersion || response.GetState().GetSchemaVersion() != toVersion {
		return fmt.Errorf("state upgrade must advance exactly one requested version")
	}
	if err := providerstate.ValidateVersioned(response.GetState(), toVersion); err != nil {
		return fmt.Errorf("state upgrade result: %w", err)
	}
	return providerstate.ValidateUpgradeRecord(response.GetRecord())
}

func newAction(id string, position uint32, actionType providerv0.ActionType, resourceType, prospectiveRemoteID string) (*providerv0.PlanAction, error) {
	if id == "" || resourceType == "" {
		return nil, fmt.Errorf("action id and resource type are required")
	}
	if (actionType == providerv0.ActionType_ACTION_TYPE_CREATE || actionType == providerv0.ActionType_ACTION_TYPE_REPLACE) && prospectiveRemoteID == "" {
		return nil, fmt.Errorf("create and replace actions require prospective remote identity")
	}
	return &providerv0.PlanAction{
		ActionId:            id,
		Position:            position,
		Type:                actionType,
		ResourceType:        resourceType,
		ProspectiveRemoteId: prospectiveRemoteID,
	}, nil
}
