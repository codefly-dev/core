package resources_test

import (
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceConfigurationOverridesRoundTripDeterministically(t *testing.T) {
	overrides := []resources.WorkspaceConfigurationOverride{
		{Name: "routing", Key: "REGION", Value: "local"},
		{Name: "execution-scheduler-auth", Key: "TOKEN", Value: "secret", Secret: true},
	}

	encoded, err := resources.EncodeWorkspaceConfigurationOverrides(overrides)
	require.NoError(t, err)
	decoded, err := resources.DecodeWorkspaceConfigurationOverrides(encoded)
	require.NoError(t, err)
	require.Equal(t, []resources.WorkspaceConfigurationOverride{
		{Name: "execution-scheduler-auth", Key: "TOKEN", Value: "secret", Secret: true},
		{Name: "routing", Key: "REGION", Value: "local"},
	}, decoded)
}

func TestWorkspaceConfigurationOverridesRejectAmbiguousCoordinates(t *testing.T) {
	_, err := resources.EncodeWorkspaceConfigurationOverrides([]resources.WorkspaceConfigurationOverride{
		{Name: "internal-auth", Key: "TOKEN", Value: "first", Secret: true},
		{Name: "internal0auth", Key: "TOKEN", Value: "unrelated", Secret: true},
		{Name: "internal_auth", Key: "token", Value: "second", Secret: true},
	})
	require.ErrorContains(t, err, "duplicate workspace configuration override")
}
