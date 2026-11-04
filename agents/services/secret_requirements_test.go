package services_test

import (
	"testing"

	"github.com/codefly-dev/core/agents/contract"
	"github.com/codefly-dev/core/agents/services"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
)

const restricted = builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1
const localApply = builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1

func declaring(required ...*builderv0.RequiredSecretConfiguration) *builderv0.BuildCapabilitiesResponse {
	return &builderv0.BuildCapabilitiesResponse{
		ExecutionContracts:           []string{services.SecretRequirementsContract},
		RequiredSecretConfigurations: required,
	}
}

func postgresGroup(profiles ...builderv0.KubernetesOutputProfile) *builderv0.RequiredSecretConfiguration {
	return &builderv0.RequiredSecretConfiguration{
		Configuration: "postgres",
		Keys:          []string{"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_READ_ONLY_PASSWORD", "POSTGRES_READ_WRITE_PASSWORD"},
		Profiles:      profiles,
	}
}

// The contract is registered as a host-supported operation contract, so an
// agent can advertise it against this Core without a version comparison.
func TestSecretRequirementsIsASupportedOperationContract(t *testing.T) {
	require.Contains(t, contract.SupportedOperationContracts(), services.SecretRequirementsContract)
	require.NotContains(t, contract.Current().GetCapabilities(), services.SecretRequirementsContract)
}

// An agent that does not advertise the contract cannot be read as requiring
// nothing: silence and "requires none" are the same empty field on the wire,
// and conflating them is the false green this exists to remove.
func TestSilenceIsNotACleanBill(t *testing.T) {
	_, err := services.MissingServiceSecretConfigurations("org-directory/store", restricted, &builderv0.BuildCapabilitiesResponse{}, nil)
	require.ErrorContains(t, err, services.SecretRequirementsContract)

	_, err = services.MissingServiceSecretConfigurations(
		"org-directory/store", restricted,
		&builderv0.BuildCapabilitiesResponse{RequiredSecretConfigurations: []*builderv0.RequiredSecretConfiguration{postgresGroup()}},
		nil,
	)
	require.ErrorContains(t, err, services.SecretRequirementsContract)
}

// A service delivering nothing is reported with every key of every group, in
// one answer — not one key per render.
func TestEveryMissingKeyIsReportedAtOnce(t *testing.T) {
	missing, err := services.MissingServiceSecretConfigurations("org-directory/store", restricted, declaring(postgresGroup()), nil)
	require.NoError(t, err)
	require.Len(t, missing, 1)
	require.Equal(t, "org-directory/store", missing[0].Service)
	require.Equal(t, "postgres", missing[0].Configuration)
	require.Equal(t, []string{
		"POSTGRES_PASSWORD", "POSTGRES_READ_ONLY_PASSWORD", "POSTGRES_READ_WRITE_PASSWORD", "POSTGRES_USER",
	}, missing[0].Keys)
	require.Equal(t, []string{
		"CODEFLY__SERVICE_SECRET_CONFIGURATION__ORG_DIRECTORY__STORE__POSTGRES__POSTGRES_PASSWORD",
		"CODEFLY__SERVICE_SECRET_CONFIGURATION__ORG_DIRECTORY__STORE__POSTGRES__POSTGRES_READ_ONLY_PASSWORD",
		"CODEFLY__SERVICE_SECRET_CONFIGURATION__ORG_DIRECTORY__STORE__POSTGRES__POSTGRES_READ_WRITE_PASSWORD",
		"CODEFLY__SERVICE_SECRET_CONFIGURATION__ORG_DIRECTORY__STORE__POSTGRES__POSTGRES_USER",
	}, missing[0].EnvironmentKeys)
	require.Contains(t, missing[0].String(), "POSTGRES_USER")
}

// The environment key is the spelling the agent's own refusal uses, so the
// doctor's message and the render's refusal name the same string.
func TestDeliveredKeysAreMatchedInTheAgentsOwnSpelling(t *testing.T) {
	delivered := []string{
		"CODEFLY__SERVICE_SECRET_CONFIGURATION__ORG_DIRECTORY__STORE__POSTGRES__POSTGRES_USER",
		"CODEFLY__SERVICE_SECRET_CONFIGURATION__ORG_DIRECTORY__STORE__POSTGRES__POSTGRES_PASSWORD",
	}
	missing, err := services.MissingServiceSecretConfigurations("org-directory/store", restricted, declaring(postgresGroup()), delivered)
	require.NoError(t, err)
	require.Len(t, missing, 1)
	require.Equal(t, []string{"POSTGRES_READ_ONLY_PASSWORD", "POSTGRES_READ_WRITE_PASSWORD"}, missing[0].Keys)
}

func TestNothingMissingReportsNothing(t *testing.T) {
	var delivered []string
	for _, key := range postgresGroup().GetKeys() {
		delivered = append(delivered, "CODEFLY__SERVICE_SECRET_CONFIGURATION__ORG_DIRECTORY__STORE__POSTGRES__"+key)
	}
	missing, err := services.MissingServiceSecretConfigurations("org-directory/store", restricted, declaring(postgresGroup()), delivered)
	require.NoError(t, err)
	require.Empty(t, missing)
}

// A credential a secret-free render references through a secretKeyRef is not
// required by a local apply, which carries the value inline.
func TestAProfileScopedRequirementOnlyAppliesToThatProfile(t *testing.T) {
	capabilities := declaring(postgresGroup(restricted))
	missing, err := services.MissingServiceSecretConfigurations("org-directory/store", restricted, capabilities, nil)
	require.NoError(t, err)
	require.Len(t, missing, 1)

	missing, err = services.MissingServiceSecretConfigurations("org-directory/store", localApply, capabilities, nil)
	require.NoError(t, err)
	require.Empty(t, missing)
}

func TestGroupsAreReportedInAStableOrder(t *testing.T) {
	capabilities := declaring(
		&builderv0.RequiredSecretConfiguration{Configuration: "second", Keys: []string{"KEY"}},
		&builderv0.RequiredSecretConfiguration{Configuration: "first", Keys: []string{"KEY"}},
	)
	missing, err := services.MissingServiceSecretConfigurations("a/b", restricted, capabilities, nil)
	require.NoError(t, err)
	require.Len(t, missing, 2)
	require.Equal(t, "first", missing[0].Configuration)
	require.Equal(t, "second", missing[1].Configuration)
}

func TestADeclarationAHostCannotActOnIsRefused(t *testing.T) {
	require.NoError(t, services.ValidateRequiredSecretConfiguration(postgresGroup()))
	require.ErrorContains(t,
		services.ValidateRequiredSecretConfiguration(&builderv0.RequiredSecretConfiguration{Keys: []string{"KEY"}}),
		"must name its group")
	require.ErrorContains(t,
		services.ValidateRequiredSecretConfiguration(&builderv0.RequiredSecretConfiguration{Configuration: "postgres"}),
		"must name its keys")
	require.ErrorContains(t,
		services.ValidateRequiredSecretConfiguration(&builderv0.RequiredSecretConfiguration{Configuration: "postgres", Keys: []string{" "}}),
		"empty key")
}
