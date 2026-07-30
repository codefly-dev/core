package provider_test

import (
	"strings"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestEveryProviderProtocolMessageRoundTrips(t *testing.T) {
	var roundTrip func(protoreflect.MessageDescriptors)
	roundTrip = func(messages protoreflect.MessageDescriptors) {
		for i := 0; i < messages.Len(); i++ {
			descriptor := messages.Get(i)
			if descriptor.IsMapEntry() {
				continue
			}
			t.Run(string(descriptor.FullName()), func(t *testing.T) {
				original := dynamicpb.NewMessage(descriptor)
				encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(original)
				require.NoError(t, err)
				decoded := dynamicpb.NewMessage(descriptor)
				require.NoError(t, proto.Unmarshal(encoded, decoded))
				require.True(t, proto.Equal(original, decoded))
			})
			roundTrip(descriptor.Messages())
		}
	}
	roundTrip(providerv0.File_codefly_services_provider_v0_provider_proto.Messages())
}

func TestProviderProtocolRetainsUnknownFieldsForWireCompatibility(t *testing.T) {
	original := &providerv0.ValidateRequest{
		Context: &providerv0.OfflineProviderContext{
			Binding: &providerv0.BindingAddress{WorkspaceId: "workspace", EnvironmentId: "environment", BindingId: "binding"},
		},
	}
	encoded, err := proto.Marshal(original)
	require.NoError(t, err)
	encoded = append(encoded, 0xa0, 0x06, 0x01)

	var decoded providerv0.ValidateRequest
	require.NoError(t, proto.Unmarshal(encoded, &decoded))
	require.Equal(t, "workspace", decoded.GetContext().GetBinding().GetWorkspaceId())
	require.NotEmpty(t, decoded.ProtoReflect().GetUnknown())
	reencoded, err := proto.Marshal(&decoded)
	require.NoError(t, err)
	var second providerv0.ValidateRequest
	require.NoError(t, proto.Unmarshal(reencoded, &second))
	require.Equal(t, decoded.ProtoReflect().GetUnknown(), second.ProtoReflect().GetUnknown())
}

func TestProviderRPCMethodPolicyIsMachineEnforceable(t *testing.T) {
	providerExpected := map[string]*providerv0.ProviderMethodPolicy{
		"GetProviderInformation": {Network: providerv0.MethodNetworkMode_METHOD_NETWORK_MODE_OFFLINE, Effect: providerv0.MethodEffect_METHOD_EFFECT_NONE},
		"Validate":               {Network: providerv0.MethodNetworkMode_METHOD_NETWORK_MODE_OFFLINE, Effect: providerv0.MethodEffect_METHOD_EFFECT_NONE},
		"Observe":                {Network: providerv0.MethodNetworkMode_METHOD_NETWORK_MODE_READ_ONLY, Effect: providerv0.MethodEffect_METHOD_EFFECT_READ_ONLY},
		"Plan":                   {Network: providerv0.MethodNetworkMode_METHOD_NETWORK_MODE_OFFLINE, Effect: providerv0.MethodEffect_METHOD_EFFECT_NONE},
		"ApplyAction":            {Network: providerv0.MethodNetworkMode_METHOD_NETWORK_MODE_ADMITTED, Effect: providerv0.MethodEffect_METHOD_EFFECT_ONE_PLAN_ACTION},
		"Doctor":                 {Network: providerv0.MethodNetworkMode_METHOD_NETWORK_MODE_READ_ONLY, Effect: providerv0.MethodEffect_METHOD_EFFECT_READ_ONLY},
		"UpgradeState":           {Network: providerv0.MethodNetworkMode_METHOD_NETWORK_MODE_OFFLINE, Effect: providerv0.MethodEffect_METHOD_EFFECT_NONE},
	}
	hostExpected := map[string]*providerv0.ProviderMethodPolicy{
		"ExecuteRequest":   {Network: providerv0.MethodNetworkMode_METHOD_NETWORK_MODE_ADMITTED, Effect: providerv0.MethodEffect_METHOD_EFFECT_ONE_PLAN_ACTION},
		"RecordCheckpoint": {Network: providerv0.MethodNetworkMode_METHOD_NETWORK_MODE_HOST_LOCAL, Effect: providerv0.MethodEffect_METHOD_EFFECT_HOST_BOOKKEEPING},
		"ResolveCapture":   {Network: providerv0.MethodNetworkMode_METHOD_NETWORK_MODE_HOST_LOCAL, Effect: providerv0.MethodEffect_METHOD_EFFECT_NONE},
		"ProposeOutput":    {Network: providerv0.MethodNetworkMode_METHOD_NETWORK_MODE_HOST_LOCAL, Effect: providerv0.MethodEffect_METHOD_EFFECT_HOST_BOOKKEEPING},
	}
	assertMethodPolicies(t, "Provider", providerExpected)
	assertMethodPolicies(t, "ProviderHost", hostExpected)
}

func assertMethodPolicies(t *testing.T, serviceName protoreflect.Name, expected map[string]*providerv0.ProviderMethodPolicy) {
	t.Helper()
	service := providerv0.File_codefly_services_provider_v0_provider_proto.Services().ByName(serviceName)
	require.NotNil(t, service)
	require.Equal(t, len(expected), service.Methods().Len())
	for i := 0; i < service.Methods().Len(); i++ {
		method := service.Methods().Get(i)
		options := method.Options().(*descriptorpb.MethodOptions)
		require.True(t, proto.HasExtension(options, providerv0.E_ProviderMethodPolicy))
		policy := proto.GetExtension(options, providerv0.E_ProviderMethodPolicy).(*providerv0.ProviderMethodPolicy)
		want := expected[string(method.Name())]
		require.NotNil(t, want, method.Name())
		require.True(t, proto.Equal(want, policy), method.Name())
	}
}

func TestOfflineRPCRequestsCannotExpressBrokerAccess(t *testing.T) {
	for _, name := range []protoreflect.Name{
		"GetProviderInformationRequest",
		"ValidateRequest",
		"PlanRequest",
		"UpgradeStateRequest",
	} {
		descriptor := providerv0.File_codefly_services_provider_v0_provider_proto.Messages().ByName(name)
		require.NotNil(t, descriptor)
		for _, forbidden := range []protoreflect.FullName{
			"codefly.services.provider.v0.ProviderContext",
			"codefly.services.provider.v0.CredentialHandle",
			"codefly.services.provider.v0.AdmittedOrigin",
			"codefly.services.provider.v0.SemanticEndpointReference",
			"codefly.services.provider.v0.ExecuteRequestRequest",
		} {
			require.False(t, reachesMessage(descriptor, forbidden, map[protoreflect.FullName]bool{}), "%s reaches %s", name, forbidden)
		}
	}
}

func TestProviderProtocolHasNoRawCredentialOrEscapeHatchPayload(t *testing.T) {
	walkMessages(providerv0.File_codefly_services_provider_v0_provider_proto.Messages(), func(descriptor protoreflect.MessageDescriptor) {
		for i := 0; i < descriptor.Fields().Len(); i++ {
			field := descriptor.Fields().Get(i)
			name := strings.ToLower(string(field.Name()))
			require.NotContains(t, name, "raw_secret", descriptor.FullName())
			require.NotContains(t, name, "credential_bytes", descriptor.FullName())
			require.NotContains(t, name, "secret_literal", descriptor.FullName())
			require.NotEqual(t, protoreflect.BytesKind, field.Kind(), "%s.%s", descriptor.FullName(), field.Name())
			if field.Kind() == protoreflect.MessageKind {
				require.NotContains(t, []protoreflect.FullName{
					"google.protobuf.Any",
					"google.protobuf.Struct",
					"google.protobuf.Value",
				}, field.Message().FullName())
			}
		}
	})
}

func TestProviderProtocolFreezesActionPaginationAndOutputVocabulary(t *testing.T) {
	action := providerv0.ActionType(0).Descriptor()
	require.Equal(t, []string{
		"ACTION_TYPE_UNSPECIFIED", "ACTION_TYPE_CREATE", "ACTION_TYPE_UPDATE", "ACTION_TYPE_REPLACE",
		"ACTION_TYPE_DELETE", "ACTION_TYPE_IMPORT", "ACTION_TYPE_MANUAL", "ACTION_TYPE_BLOCKED",
		"ACTION_TYPE_NO_OP", "ACTION_TYPE_PROJECT_OUTPUT",
	}, enumNames(action.Values()))

	observation := (&providerv0.MaterialObservation{}).ProtoReflect().Descriptor()
	require.NotNil(t, observation.Fields().ByName("complete"))
	require.NotNil(t, observation.Fields().ByName("next_cursor"))

	output := (&providerv0.OutputValue{}).ProtoReflect().Descriptor()
	require.Equal(t, 1, output.Oneofs().Len())
	require.Equal(t, []string{"public_value", "opaque_reference"}, fieldNames(output.Oneofs().Get(0).Fields()))
}

func reachesMessage(descriptor protoreflect.MessageDescriptor, target protoreflect.FullName, visited map[protoreflect.FullName]bool) bool {
	if descriptor.FullName() == target {
		return true
	}
	if visited[descriptor.FullName()] {
		return false
	}
	visited[descriptor.FullName()] = true
	for i := 0; i < descriptor.Fields().Len(); i++ {
		field := descriptor.Fields().Get(i)
		if field.Kind() == protoreflect.MessageKind && reachesMessage(field.Message(), target, visited) {
			return true
		}
	}
	return false
}

func walkMessages(messages protoreflect.MessageDescriptors, visit func(protoreflect.MessageDescriptor)) {
	for i := 0; i < messages.Len(); i++ {
		descriptor := messages.Get(i)
		if descriptor.IsMapEntry() {
			continue
		}
		visit(descriptor)
		walkMessages(descriptor.Messages(), visit)
	}
}

func enumNames(values protoreflect.EnumValueDescriptors) []string {
	out := make([]string, values.Len())
	for i := 0; i < values.Len(); i++ {
		out[i] = string(values.Get(i).Name())
	}
	return out
}

func fieldNames(fields protoreflect.FieldDescriptors) []string {
	out := make([]string, fields.Len())
	for i := 0; i < fields.Len(); i++ {
		out[i] = string(fields.Get(i).Name())
	}
	return out
}
