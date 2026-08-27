package broker

import (
	"fmt"
	"strings"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/network/urlguard"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/codefly-dev/core/provider/responsepolicy"
)

// purposeEnum maps a manifest credential-purpose consumer to the proto enum.
func purposeEnum(consumer string) (providerv0.CredentialPurpose, error) {
	switch consumer {
	case "management":
		return providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT, nil
	case "runtime":
		return providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME, nil
	case "build":
		return providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_BUILD, nil
	case "webhook-verification":
		return providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_WEBHOOK_VERIFICATION, nil
	default:
		return providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_UNSPECIFIED, fmt.Errorf("unknown credential purpose consumer %q", consumer)
	}
}

// classEnum maps a manifest private-network-class token to the proto enum.
func classEnum(class urlguard.NetworkClass) providerv0.PrivateNetworkClass {
	switch class {
	case urlguard.ClassLoopback:
		return providerv0.PrivateNetworkClass_PRIVATE_NETWORK_CLASS_LOOPBACK
	case urlguard.ClassLinkLocal:
		return providerv0.PrivateNetworkClass_PRIVATE_NETWORK_CLASS_LINK_LOCAL
	case urlguard.ClassPrivate:
		return providerv0.PrivateNetworkClass_PRIVATE_NETWORK_CLASS_PRIVATE
	default:
		return providerv0.PrivateNetworkClass_PRIVATE_NETWORK_CLASS_PUBLIC
	}
}

// networkClasses maps manifest class tokens to urlguard classes.
func networkClasses(tokens []string) []urlguard.NetworkClass {
	classes := make([]urlguard.NetworkClass, 0, len(tokens))
	for _, token := range tokens {
		switch token {
		case "loopback":
			classes = append(classes, urlguard.ClassLoopback)
		case "link-local":
			classes = append(classes, urlguard.ClassLinkLocal)
		case "private":
			classes = append(classes, urlguard.ClassPrivate)
		}
	}
	return classes
}

// ceilingFor builds the urlguard ceiling for a manifest origin rule.
func ceilingFor(rule manifest.OriginRule) urlguard.Ceiling {
	return urlguard.Ceiling{
		Schemes:        append([]string(nil), rule.Schemes...),
		HostPatterns:   append([]string(nil), rule.HostPatterns...),
		Ports:          append([]uint32(nil), rule.Ports...),
		AllowedClasses: networkClasses(rule.PrivateNetworkClasses),
	}
}

// responsePolicyFor derives the host response policy from the manifest response
// schema a descriptor references. Capture selectors that address an exact path
// are required (a planned single secret that vanishes is drift); wildcard
// captures are optional because an empty collection legitimately has none.
func (s *Session) responsePolicyFor(descriptor manifest.RequestDescriptor) (responsepolicy.Policy, error) {
	schema, ok := s.responseSchemas[descriptor.ResponseSchema]
	if !ok {
		return responsepolicy.Policy{}, fmt.Errorf("response schema %q is not packaged", descriptor.ResponseSchema)
	}
	fields := make([]responsepolicy.Field, 0, len(schema.Fields))
	for _, field := range schema.Fields {
		policyField := responsepolicy.Field{
			Selector:    field.Selector,
			Disposition: field.Disposition,
		}
		if field.Disposition == manifest.ResponseCaptureToSink {
			purpose, ok := s.purposes[field.Purpose]
			if !ok {
				return responsepolicy.Policy{}, fmt.Errorf("capture purpose %q is not packaged", field.Purpose)
			}
			enum, err := purposeEnum(purpose.PermittedConsumer)
			if err != nil {
				return responsepolicy.Policy{}, err
			}
			policyField.Purpose = enum
			policyField.Required = !strings.Contains(field.Selector.Path, "[*]")
			policyField.SinkKey = fmt.Sprintf("%s/%s/%s", s.binding.GetBindingId(), descriptor.ID, field.Selector.Path)
		}
		fields = append(fields, policyField)
	}
	return responsepolicy.Policy{Fields: fields, Limits: s.limits}, nil
}

// streamPolicyFor derives the per-event response policy for a streamed response.
// It is the descriptor's response policy with every field made non-required: a
// declared field legitimately appears in only some events of a stream, so its
// absence from a given event is not drift. Required-ness is a whole-body drift
// check that does not survive being split across a stream; the security
// invariant — every present secret-shaped field is captured or suppressed and
// every undeclared field dropped — holds unchanged per event.
func (s *Session) streamPolicyFor(descriptor manifest.RequestDescriptor) (responsepolicy.Policy, error) {
	policy, err := s.responsePolicyFor(descriptor)
	if err != nil {
		return responsepolicy.Policy{}, err
	}
	for i := range policy.Fields {
		policy.Fields[i].Required = false
	}
	return policy, nil
}

// plannedRemoteID returns the exact remote identity the action authorizes. Path
// parameters and ownership body fields must equal this value; a valid template
// carrying any other id is rejected.
func plannedRemoteID(action *providerv0.PlanAction) (string, error) {
	switch action.GetType() {
	case providerv0.ActionType_ACTION_TYPE_CREATE, providerv0.ActionType_ACTION_TYPE_REPLACE:
		if id := action.GetProspectiveRemoteId(); id != "" {
			return id, nil
		}
	default:
		if id := action.GetRemoteIdentity().GetRemoteId(); id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("action has no bound remote identity")
}
