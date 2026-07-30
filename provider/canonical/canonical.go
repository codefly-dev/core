package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"sort"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/configuration"
	"github.com/codefly-dev/core/provider/manifest"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var (
	decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$`)
	digestPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

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
	if err := sortByCanonicalBytes(clone.CredentialReferences); err != nil {
		return "", err
	}
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
	if err := sortByCanonicalBytes(clone.Resources); err != nil {
		return "", err
	}
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
	for index, request := range action.GetRequests() {
		computed, err := PlannedRequestDigest(request)
		if err != nil {
			return fmt.Errorf("planned request %d: %w", index, err)
		}
		if request.GetRequestDigest() != computed {
			return fmt.Errorf("planned request %d digest mismatch", index)
		}
	}
	if action.GetType() == providerv0.ActionType_ACTION_TYPE_PROJECT_OUTPUT {
		if len(action.GetRequests()) != 0 {
			return fmt.Errorf("project output action cannot contain broker requests")
		}
		output := action.GetOutput()
		if output == nil || output.GetContract() == "" || output.GetTargetGeneration() == 0 {
			return fmt.Errorf("project output action requires an exact output proposal")
		}
		computed, err := OutputProposalDigest(output)
		if err != nil {
			return err
		}
		if output.GetDigest() != computed {
			return fmt.Errorf("project output proposal digest mismatch")
		}
	} else if action.GetOutput() != nil {
		return fmt.Errorf("only project output actions may contain an output proposal")
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

func PlannedRequestDigest(request *providerv0.PlannedRequest) (string, error) {
	if request == nil {
		return "", fmt.Errorf("planned request is required")
	}
	if request.GetRequestDescriptorId() == "" ||
		!digestPattern.MatchString(request.GetRequestDescriptorDigest()) ||
		!digestPattern.MatchString(request.GetAdmittedOriginDigest()) ||
		!digestPattern.MatchString(request.GetResponsePolicyDigest()) {
		return "", fmt.Errorf("planned request descriptor, origin, and response policy are required")
	}
	switch request.GetMethod() {
	case providerv0.HTTPMethod_HTTP_METHOD_GET, providerv0.HTTPMethod_HTTP_METHOD_HEAD:
	case providerv0.HTTPMethod_HTTP_METHOD_POST, providerv0.HTTPMethod_HTTP_METHOD_PUT,
		providerv0.HTTPMethod_HTTP_METHOD_PATCH, providerv0.HTTPMethod_HTTP_METHOD_DELETE:
		if request.GetIdempotencyKey() == "" {
			return "", fmt.Errorf("mutating planned request requires an idempotency key")
		}
	default:
		return "", fmt.Errorf("unknown planned request method %d", request.GetMethod())
	}
	seenPurposes := make(map[providerv0.CredentialPurpose]struct{}, len(request.GetCredentialPurposes()))
	for _, purpose := range request.GetCredentialPurposes() {
		switch purpose {
		case providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT,
			providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
			providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_BUILD,
			providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_WEBHOOK_VERIFICATION:
		default:
			return "", fmt.Errorf("unknown planned request credential purpose %d", purpose)
		}
		if _, duplicate := seenPurposes[purpose]; duplicate {
			return "", fmt.Errorf("planned request credential purpose %s is duplicated", purpose)
		}
		seenPurposes[purpose] = struct{}{}
	}
	clone := proto.Clone(request).(*providerv0.PlannedRequest)
	clone.RequestDigest = ""
	// Credential purposes are a set (duplicates are rejected above and
	// ValidateExecuteRequest compares them unordered), so the digest must not
	// depend on the order in which they were listed.
	slices.Sort(clone.CredentialPurposes)
	return Digest(clone)
}

func BindPlannedRequestDigest(request *providerv0.PlannedRequest) (*providerv0.PlannedRequest, error) {
	if request == nil {
		return nil, fmt.Errorf("planned request is required")
	}
	clone := proto.Clone(request).(*providerv0.PlannedRequest)
	computed, err := PlannedRequestDigest(clone)
	if err != nil {
		return nil, err
	}
	if clone.GetRequestDigest() != "" && clone.GetRequestDigest() != computed {
		return nil, fmt.Errorf("planned request digest mismatch")
	}
	clone.RequestDigest = computed
	return clone, nil
}

func ValidateExecuteRequest(action *providerv0.PlanAction, request *providerv0.ExecuteRequestRequest) error {
	if err := ValidatePlanAction(action); err != nil {
		return err
	}
	if request == nil || request.GetContext() == nil || request.GetRequest() == nil || request.GetOrigin() == nil {
		return fmt.Errorf("execute request context, planned request, and origin are required")
	}
	operation := request.GetContext().GetOperation()
	if operation == nil || operation.GetActionId() != action.GetActionId() {
		return fmt.Errorf("execute request action identity does not match admitted action")
	}
	plannedDigest, err := PlannedRequestDigest(request.GetRequest())
	if err != nil {
		return err
	}
	if request.GetRequest().GetRequestDigest() != plannedDigest {
		return fmt.Errorf("execute request digest mismatch")
	}
	admitted := false
	for _, candidate := range action.GetRequests() {
		if candidate.GetRequestDigest() == plannedDigest {
			admitted = true
			break
		}
	}
	if !admitted {
		return fmt.Errorf("execute request is not present in admitted action")
	}
	originDigest, err := AdmittedOriginDigest(request.GetOrigin())
	if err != nil {
		return err
	}
	if originDigest != request.GetRequest().GetAdmittedOriginDigest() ||
		request.GetOrigin().GetAdmissionDigest() != originDigest {
		return fmt.Errorf("execute request origin does not match admitted origin")
	}
	contextHandles := make(map[string]providerv0.CredentialPurpose, len(request.GetContext().GetCredentials()))
	for _, handle := range request.GetContext().GetCredentials() {
		if handle.GetHandle() == "" {
			return fmt.Errorf("provider context contains an empty credential handle")
		}
		contextHandles[handle.GetHandle()] = handle.GetPurpose()
	}
	requestPurposes := make(map[providerv0.CredentialPurpose]struct{}, len(request.GetCredentialHandles()))
	for _, handle := range request.GetCredentialHandles() {
		purpose, exists := contextHandles[handle.GetHandle()]
		if !exists || purpose != handle.GetPurpose() {
			return fmt.Errorf("execute request credential handle is not present in provider context")
		}
		if _, duplicate := requestPurposes[purpose]; duplicate {
			return fmt.Errorf("execute request credential purpose %s is duplicated", purpose)
		}
		requestPurposes[purpose] = struct{}{}
	}
	if len(requestPurposes) != len(request.GetRequest().GetCredentialPurposes()) {
		return fmt.Errorf("execute request credential purposes do not match admitted request")
	}
	for _, purpose := range request.GetRequest().GetCredentialPurposes() {
		if _, exists := requestPurposes[purpose]; !exists {
			return fmt.Errorf("execute request credential purposes do not match admitted request")
		}
	}
	return nil
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
	if message.Descriptor().FullName() == "codefly.services.provider.v0.OpaqueReference" {
		reference := message.Interface().(*providerv0.OpaqueReference)
		if err := configuration.ValidateOpaqueReference(reference.GetReference()); err != nil {
			return err
		}
		switch reference.GetPurpose() {
		case providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT,
			providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
			providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_BUILD,
			providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_WEBHOOK_VERIFICATION:
		default:
			return fmt.Errorf("opaque reference has unknown credential purpose")
		}
		if reference.GetSafeFingerprint() != "" && !digestPattern.MatchString(reference.GetSafeFingerprint()) {
			return fmt.Errorf("opaque reference safe fingerprint is not canonical")
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

// sortByCanonicalBytes orders a repeated message field by each element's
// deterministic wire encoding. This is a total order over the full content of
// every element, so a digest computed over the sorted slice is independent of
// the order in which elements were supplied — including when two elements share
// a partial identity but differ elsewhere, which a partial sort key left
// permutation-dependent.
func sortByCanonicalBytes[T proto.Message](items []T) error {
	if len(items) < 2 {
		return nil
	}
	type keyed struct {
		key  string
		item T
	}
	entries := make([]keyed, len(items))
	for i, item := range items {
		data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(item)
		if err != nil {
			return err
		}
		entries[i] = keyed{key: string(data), item: item}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	for i := range entries {
		items[i] = entries[i].item
	}
	return nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
