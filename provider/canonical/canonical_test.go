package canonical_test

import (
	"strings"
	"testing"

	basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"
	providerv1 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v1"
	"github.com/codefly-dev/core/provider/canonical"
	"github.com/codefly-dev/core/provider/configuration"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestCanonicalMapAndResourceOrderingIsByteIdentical(t *testing.T) {
	first := materialObservation()
	second := proto.Clone(first).(*providerv1.MaterialObservation)
	second.Resources[0], second.Resources[1] = second.Resources[1], second.Resources[0]
	second.Resources[0].ProviderOwnedFields = map[string]*providerv1.PublicValue{
		"z": stringValue("last"),
		"a": stringValue("first"),
	}
	first.Resources[1].ProviderOwnedFields = map[string]*providerv1.PublicValue{
		"a": stringValue("first"),
		"z": stringValue("last"),
	}
	firstDigest, err := canonical.MaterialObservationDigest(first)
	require.NoError(t, err)
	secondDigest, err := canonical.MaterialObservationDigest(second)
	require.NoError(t, err)
	require.Equal(t, firstDigest, secondDigest)
}

func TestMaterialDigestIsOrderIndependentWhenResourcesShareIdentity(t *testing.T) {
	// Two distinct observations that share a remote identity but differ in
	// content: a partial identity sort key left their relative order — and thus
	// the digest — dependent on retrieval order.
	identity := &providerv1.RemoteIdentity{Provider: "fixture", AccountId: "account", ResourceType: "account", RemoteId: "shared"}
	first := &providerv1.MaterialObservation{
		AccountIdentity: "account", Mode: providerv1.HostMode_HOST_MODE_PRODUCTION, Complete: true,
		Resources: []*providerv1.MaterialResourceObservation{
			{Identity: proto.Clone(identity).(*providerv1.RemoteIdentity), Ownership: providerv1.Ownership_OWNERSHIP_OWNED, Revision: "1"},
			{Identity: proto.Clone(identity).(*providerv1.RemoteIdentity), Ownership: providerv1.Ownership_OWNERSHIP_OWNED, Revision: "2"},
		},
	}
	second := proto.Clone(first).(*providerv1.MaterialObservation)
	second.Resources[0], second.Resources[1] = second.Resources[1], second.Resources[0]

	firstDigest, err := canonical.MaterialObservationDigest(first)
	require.NoError(t, err)
	secondDigest, err := canonical.MaterialObservationDigest(second)
	require.NoError(t, err)
	require.Equal(t, firstDigest, secondDigest)
}

func TestBindingDesiredDigestIsOrderIndependentWhenReferencesTieOnPurposeAndReference(t *testing.T) {
	// Credential references that tie on (purpose, reference) but differ in
	// safe_fingerprint used to sort permutation-dependently.
	reference := func(fingerprint string) *providerv1.OpaqueReference {
		return &providerv1.OpaqueReference{
			Reference: "secret://runtime", Purpose: providerv1.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
			SafeFingerprint: digest(fingerprint),
		}
	}
	first := &providerv1.BindingDesiredState{
		CredentialReferences: []*providerv1.OpaqueReference{reference("1"), reference("2")},
	}
	second := &providerv1.BindingDesiredState{
		CredentialReferences: []*providerv1.OpaqueReference{reference("2"), reference("1")},
	}
	firstDigest, err := canonical.BindingDesiredStateDigest(first)
	require.NoError(t, err)
	secondDigest, err := canonical.BindingDesiredStateDigest(second)
	require.NoError(t, err)
	require.Equal(t, firstDigest, secondDigest)
}

func TestPlannedRequestDigestIsCredentialPurposeOrderIndependent(t *testing.T) {
	base := &providerv1.PlannedRequest{
		RequestDescriptorId:     "account.read",
		RequestDescriptorDigest: digest("1"),
		Method:                  providerv1.HTTPMethod_HTTP_METHOD_GET,
		AdmittedOriginDigest:    digest("1"),
		ResponsePolicyDigest:    digest("1"),
		CredentialPurposes: []providerv1.CredentialPurpose{
			providerv1.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT,
			providerv1.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME,
		},
	}
	reordered := proto.Clone(base).(*providerv1.PlannedRequest)
	reordered.CredentialPurposes[0], reordered.CredentialPurposes[1] = reordered.CredentialPurposes[1], reordered.CredentialPurposes[0]

	baseDigest, err := canonical.PlannedRequestDigest(base)
	require.NoError(t, err)
	reorderedDigest, err := canonical.PlannedRequestDigest(reordered)
	require.NoError(t, err)
	require.Equal(t, baseDigest, reorderedDigest)
}

func TestFeatureFlagsOutputProposalDigestIsValueOrderIndependent(t *testing.T) {
	endpoint := outputPublicValue("endpoint://feature-flags/server")
	application := outputPublicValue("accounts")
	environment := outputPublicValue("production")
	mode := outputPublicValue("hosted")
	credential := outputOpaqueValue("secret://feature-flags/server", providerv1.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME)
	credential.GetOpaqueReference().SafeFingerprint = digest("b")
	first := &providerv1.OutputProposal{
		Contract: configuration.FeatureFlagsContract, TargetGeneration: 1,
		Values: map[string]*providerv1.OutputValue{
			"FEATURE_FLAGS_SERVER_ENDPOINT":   endpoint,
			"FEATURE_FLAGS_APPLICATION_ID":    application,
			"FEATURE_FLAGS_ENVIRONMENT_ID":    environment,
			"FEATURE_FLAGS_PROVIDER_MODE":     mode,
			"FEATURE_FLAGS_SERVER_CREDENTIAL": credential,
		},
	}
	second := &providerv1.OutputProposal{
		Contract: configuration.FeatureFlagsContract, TargetGeneration: 1,
		Values: map[string]*providerv1.OutputValue{
			"FEATURE_FLAGS_SERVER_CREDENTIAL": proto.Clone(credential).(*providerv1.OutputValue),
			"FEATURE_FLAGS_PROVIDER_MODE":     proto.Clone(mode).(*providerv1.OutputValue),
			"FEATURE_FLAGS_ENVIRONMENT_ID":    proto.Clone(environment).(*providerv1.OutputValue),
			"FEATURE_FLAGS_APPLICATION_ID":    proto.Clone(application).(*providerv1.OutputValue),
			"FEATURE_FLAGS_SERVER_ENDPOINT":   proto.Clone(endpoint).(*providerv1.OutputValue),
		},
	}

	firstDigest, err := canonical.OutputProposalDigest(first)
	require.NoError(t, err)
	secondDigest, err := canonical.OutputProposalDigest(second)
	require.NoError(t, err)
	require.Equal(t, firstDigest, secondDigest)
}

func TestVolatileObservationMetadataDoesNotChangeMaterialDigest(t *testing.T) {
	material := materialObservation()
	first := &providerv1.ObserveResponse{
		Material: material,
		Volatile: &providerv1.VolatileObservation{
			RequestIds: []string{"request-b", "request-a"}, RateLimitRemaining: 10,
			Diagnostics: []*basev1.FailureDiagnostic{{Code: "z"}, {Code: "a"}},
		},
	}
	second := proto.Clone(first).(*providerv1.ObserveResponse)
	second.Volatile.RequestIds = []string{"request-c"}
	second.Volatile.RateLimitRemaining = 1
	second.Volatile.Diagnostics[0], second.Volatile.Diagnostics[1] = second.Volatile.Diagnostics[1], second.Volatile.Diagnostics[0]
	firstDigest, err := canonical.ObserveResponseMaterialDigest(first)
	require.NoError(t, err)
	secondDigest, err := canonical.ObserveResponseMaterialDigest(second)
	require.NoError(t, err)
	require.Equal(t, firstDigest, secondDigest)

	second.Material.Resources[0].Revision = "changed"
	secondDigest, err = canonical.ObserveResponseMaterialDigest(second)
	require.NoError(t, err)
	require.NotEqual(t, firstDigest, secondDigest)
}

func TestOrderedPlanDigestBindsEveryMaterialInputAndOrder(t *testing.T) {
	plan := orderedPlan()
	bound, err := canonical.BindOrderedPlanDigest(plan)
	require.NoError(t, err)
	repeated, err := canonical.BindOrderedPlanDigest(bound)
	require.NoError(t, err)
	require.Equal(t, bound.PlanDigest, repeated.PlanDigest)

	mutations := map[string]func(*providerv1.OrderedPlan){
		"artifact":   func(plan *providerv1.OrderedPlan) { plan.ArtifactDigest = digest("2") },
		"descriptor": func(plan *providerv1.OrderedPlan) { plan.Actions[0].Requests[0].RequestDescriptorDigest = digest("3") },
		"origin":     func(plan *providerv1.OrderedPlan) { plan.Actions[0].Requests[0].AdmittedOriginDigest = digest("3") },
		"path": func(plan *providerv1.OrderedPlan) {
			plan.Actions[0].Requests[0].PathParameters["account_id"] = stringValue("other")
		},
		"query": func(plan *providerv1.OrderedPlan) { plan.Actions[0].Requests[0].Query["expand"] = stringValue("other") },
		"body":  func(plan *providerv1.OrderedPlan) { plan.Actions[0].Requests[0].Body["name"] = stringValue("other") },
		"credential purpose": func(plan *providerv1.OrderedPlan) {
			plan.Actions[0].Requests[0].CredentialPurposes[0] = providerv1.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME
		},
		"response policy":   func(plan *providerv1.OrderedPlan) { plan.Actions[0].Requests[0].ResponsePolicyDigest = digest("3") },
		"output generation": func(plan *providerv1.OrderedPlan) { plan.OutputTargetDigest = digest("4") },
		"output proposal":   func(plan *providerv1.OrderedPlan) { plan.Actions[1].Output.TargetGeneration++ },
		"policy":            func(plan *providerv1.OrderedPlan) { plan.PolicyInputDigest = digest("5") },
		"action order": func(plan *providerv1.OrderedPlan) {
			plan.Actions[0], plan.Actions[1] = plan.Actions[1], plan.Actions[0]
			plan.Actions[0].Position, plan.Actions[1].Position = 0, 1
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := proto.Clone(bound).(*providerv1.OrderedPlan)
			changed.PlanDigest = ""
			mutate(changed)
			for _, action := range changed.Actions {
				for index, request := range action.Requests {
					request.RequestDigest = ""
					action.Requests[index], err = canonical.BindPlannedRequestDigest(request)
					require.NoError(t, err)
				}
				if action.Output != nil {
					action.Output.Digest = ""
					action.Output.Digest, err = canonical.OutputProposalDigest(action.Output)
					require.NoError(t, err)
				}
			}
			changedDigest, err := canonical.OrderedPlanDigest(changed)
			require.NoError(t, err)
			require.NotEqual(t, bound.PlanDigest, changedDigest)
		})
	}
}

func TestCanonicalRejectsUnknownFieldsAndAmbiguousValues(t *testing.T) {
	value := stringValue("safe")
	encoded, err := proto.Marshal(value)
	require.NoError(t, err)
	encoded = append(encoded, 0xa0, 0x06, 0x01)
	var unknown providerv1.PublicValue
	require.NoError(t, proto.Unmarshal(encoded, &unknown))
	_, err = canonical.Digest(&unknown)
	require.ErrorContains(t, err, "unknown fields")

	for _, decimal := range []string{"01", "1.0", "-0", "1e3", "NaN"} {
		_, err := canonical.Digest(&providerv1.PublicValue{
			Kind: &providerv1.PublicValue_DecimalValue{DecimalValue: decimal},
		})
		require.ErrorContains(t, err, "not canonical")
	}
	_, err = canonical.Digest(stringValue("sk_live_1234567890abcdef"))
	require.ErrorContains(t, err, "secret-shaped")

	plan := orderedPlan()
	plan.Actions[0].Type = providerv1.ActionType(99)
	_, err = canonical.OrderedPlanDigest(plan)
	require.ErrorContains(t, err, "unknown plan action")
}

func TestExecuteRequestMustMatchExactAdmittedPlanRequest(t *testing.T) {
	plan := orderedPlan()
	action := plan.Actions[0]
	origin := &providerv1.AdmittedOrigin{
		OriginRuleId: "api", Scheme: "https", Host: "api.example.com", Port: 443,
		PrivateNetworkClass: providerv1.PrivateNetworkClass_PRIVATE_NETWORK_CLASS_PUBLIC,
	}
	var err error
	origin.AdmissionDigest, err = canonical.AdmittedOriginDigest(origin)
	require.NoError(t, err)
	action.Requests[0].AdmittedOriginDigest = origin.AdmissionDigest
	action.Requests[0].RequestDigest = ""
	action.Requests[0], err = canonical.BindPlannedRequestDigest(action.Requests[0])
	require.NoError(t, err)
	handle := &providerv1.CredentialHandle{
		Handle: "host-handle", Purpose: providerv1.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT,
	}
	execute := &providerv1.ExecuteRequestRequest{
		Context: &providerv1.ProviderContext{
			Operation:   &providerv1.OperationIdentity{ActionId: action.ActionId},
			Credentials: []*providerv1.CredentialHandle{handle},
		},
		Request:           proto.Clone(action.Requests[0]).(*providerv1.PlannedRequest),
		Origin:            origin,
		CredentialHandles: []*providerv1.CredentialHandle{handle},
	}
	require.NoError(t, canonical.ValidateExecuteRequest(action, execute))

	execute.Request.Body["name"] = stringValue("different")
	require.ErrorContains(t, canonical.ValidateExecuteRequest(action, execute), "digest mismatch")
	execute.Request.RequestDigest = ""
	execute.Request, err = canonical.BindPlannedRequestDigest(execute.Request)
	require.NoError(t, err)
	require.ErrorContains(t, canonical.ValidateExecuteRequest(action, execute), "not present in admitted action")
}

func materialObservation() *providerv1.MaterialObservation {
	return &providerv1.MaterialObservation{
		AccountIdentity: "account",
		Mode:            providerv1.HostMode_HOST_MODE_PRODUCTION,
		Complete:        true,
		Resources: []*providerv1.MaterialResourceObservation{
			{
				Identity:  &providerv1.RemoteIdentity{Provider: "fixture", AccountId: "account", ResourceType: "account", RemoteId: "b"},
				Ownership: providerv1.Ownership_OWNERSHIP_OWNED, Revision: "1",
			},
			{
				Identity:  &providerv1.RemoteIdentity{Provider: "fixture", AccountId: "account", ResourceType: "account", RemoteId: "a"},
				Ownership: providerv1.Ownership_OWNERSHIP_OWNED, Revision: "1",
			},
		},
	}
}

func orderedPlan() *providerv1.OrderedPlan {
	request, err := canonical.BindPlannedRequestDigest(&providerv1.PlannedRequest{
		RequestDescriptorId:     "account.update",
		RequestDescriptorDigest: digest("1"),
		Method:                  providerv1.HTTPMethod_HTTP_METHOD_PATCH,
		AdmittedOriginDigest:    digest("1"),
		PathParameters:          map[string]*providerv1.PublicValue{"account_id": stringValue("account")},
		Query:                   map[string]*providerv1.PublicValue{"expand": stringValue("status")},
		Body:                    map[string]*providerv1.PublicValue{"name": stringValue("updated")},
		CredentialPurposes:      []providerv1.CredentialPurpose{providerv1.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT},
		ResponsePolicyDigest:    digest("1"),
		IdempotencyKey:          "idempotency-1",
	})
	if err != nil {
		panic(err)
	}
	output := billingOutputProposal()
	output.Digest, err = canonical.OutputProposalDigest(output)
	if err != nil {
		panic(err)
	}
	return &providerv1.OrderedPlan{
		PlanId: "plan", ArtifactDigest: digest("1"), ManifestDigest: digest("1"), CatalogDigest: digest("1"),
		DesiredDigest: digest("1"), ObservationDigest: digest("1"), OutputTargetDigest: digest("1"),
		StateGenerationDigest: digest("1"), PolicyInputDigest: digest("1"),
		Actions: []*providerv1.PlanAction{
			{ActionId: "first", Position: 0, Type: providerv1.ActionType_ACTION_TYPE_UPDATE, ResourceType: "account", Requests: []*providerv1.PlannedRequest{request}},
			{ActionId: "second", Position: 1, Type: providerv1.ActionType_ACTION_TYPE_PROJECT_OUTPUT, ResourceType: "project-output", Output: output},
		},
	}
}

func stringValue(value string) *providerv1.PublicValue {
	return &providerv1.PublicValue{Kind: &providerv1.PublicValue_StringValue{StringValue: value}}
}

func outputPublicValue(value string) *providerv1.OutputValue {
	return &providerv1.OutputValue{Kind: &providerv1.OutputValue_PublicValue{PublicValue: stringValue(value)}}
}

func outputOpaqueValue(reference string, purpose providerv1.CredentialPurpose) *providerv1.OutputValue {
	return &providerv1.OutputValue{Kind: &providerv1.OutputValue_OpaqueReference{OpaqueReference: &providerv1.OpaqueReference{
		Reference: reference, Purpose: purpose,
	}}}
}

func billingOutputProposal() *providerv1.OutputProposal {
	return &providerv1.OutputProposal{
		Contract: configuration.BillingContract, TargetGeneration: 2,
		Values: map[string]*providerv1.OutputValue{
			"STRIPE_PUBLISHABLE_KEY": outputPublicValue("pk_live_public_identifier"),
			"STRIPE_SECRET_KEY":      outputOpaqueValue("secret://stripe/runtime", providerv1.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME),
			"STRIPE_WEBHOOK_SECRET":  outputOpaqueValue("secret://stripe/webhook", providerv1.CredentialPurpose_CREDENTIAL_PURPOSE_WEBHOOK_VERIFICATION),
		},
	}
}

func digest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
