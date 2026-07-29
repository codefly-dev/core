package services

import (
	"reflect"
	"strings"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// deliverySystemTerms names transport, reconciliation, and promotion mechanisms
// a manifest-producing plugin must never encode in its contract. A plugin emits
// a deterministic manifest bundle; a separate deployment component decides how
// that bundle is delivered.
var deliverySystemTerms = []string{
	"gitops",
	"git",
	"github",
	"gitlab",
	"argo",
	"flux",
	"repository",
	"repo",
	"reconcile",
	"reconciler",
	"promotable",
	"promotion",
	"promote",
	"branch",
	"commit",
	"pullrequest",
	"pull_request",
}

func bannedDeliveryTerm(name string) string {
	lowered := strings.ToLower(name)
	for _, term := range deliverySystemTerms {
		if strings.Contains(lowered, term) {
			return term
		}
	}
	return ""
}

// TestKubernetesPluginContractHasNoDeliverySystemTerms proves the active
// plugin-facing deployment contract names no delivery mechanism. Legacy
// identifiers are permitted only while explicitly marked deprecated, which
// scopes them to the documented migration window.
func TestKubernetesPluginContractHasNoDeliverySystemTerms(t *testing.T) {
	file := builderv0.File_codefly_services_builder_v0_deployment_proto

	enums := file.Enums()
	for i := 0; i < enums.Len(); i++ {
		assertEnumNeutral(t, enums.Get(i))
	}
	messages := file.Messages()
	for i := 0; i < messages.Len(); i++ {
		assertMessageNeutral(t, messages.Get(i))
	}
}

func assertMessageNeutral(t *testing.T, message protoreflect.MessageDescriptor) {
	t.Helper()
	deprecated := false
	if options, ok := message.Options().(*descriptorpb.MessageOptions); ok {
		deprecated = options.GetDeprecated()
	}
	if !deprecated {
		if term := bannedDeliveryTerm(string(message.Name())); term != "" {
			t.Errorf("message %s names delivery mechanism %q in the active contract", message.FullName(), term)
		}
	}
	fields := message.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		fieldDeprecated := deprecated
		if options, ok := field.Options().(*descriptorpb.FieldOptions); ok && options.GetDeprecated() {
			fieldDeprecated = true
		}
		if fieldDeprecated {
			continue
		}
		if term := bannedDeliveryTerm(string(field.Name())); term != "" {
			t.Errorf("field %s names delivery mechanism %q in the active contract", field.FullName(), term)
		}
	}
	nested := message.Messages()
	for i := 0; i < nested.Len(); i++ {
		assertMessageNeutral(t, nested.Get(i))
	}
	nestedEnums := message.Enums()
	for i := 0; i < nestedEnums.Len(); i++ {
		assertEnumNeutral(t, nestedEnums.Get(i))
	}
}

func assertEnumNeutral(t *testing.T, enum protoreflect.EnumDescriptor) {
	t.Helper()
	values := enum.Values()
	for i := 0; i < values.Len(); i++ {
		value := values.Get(i)
		if options, ok := value.Options().(*descriptorpb.EnumValueOptions); ok && options.GetDeprecated() {
			continue
		}
		if term := bannedDeliveryTerm(string(value.Name())); term != "" {
			t.Errorf("enum value %s.%s names delivery mechanism %q in the active contract", enum.FullName(), value.Name(), term)
		}
	}
}

// TestKubernetesRenderingInputsHaveNoDeliverySystemFields proves the shared
// rendering inputs handed to plugin templates expose no delivery-system flag.
func TestKubernetesRenderingInputsHaveNoDeliverySystemFields(t *testing.T) {
	for _, target := range []reflect.Type{
		reflect.TypeFor[DeploymentBase](),
		reflect.TypeFor[DeploymentWrapper](),
		reflect.TypeFor[DeploymentParameters](),
	} {
		for i := 0; i < target.NumField(); i++ {
			field := target.Field(i)
			if term := bannedDeliveryTerm(field.Name); term != "" {
				t.Errorf("%s.%s names delivery mechanism %q in a plugin-facing rendering input", target.Name(), field.Name, term)
			}
		}
	}
}

// TestRestrictedProfileHasNoDeliverySystemDependencies asserts the restricted
// contract is a self-contained enum property with no companion transport type.
func TestRestrictedProfileHasNoDeliverySystemDependencies(t *testing.T) {
	require.True(t, IsRestrictedOutputProfile(builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1))
	require.True(t, IsRestrictedOutputProfile(builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1)) //nolint:staticcheck // migration compatibility
	require.False(t, IsRestrictedOutputProfile(builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1))
	require.False(t, IsRestrictedOutputProfile(builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_UNSPECIFIED))
}
