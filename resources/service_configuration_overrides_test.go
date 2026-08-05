package resources_test

import (
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestServiceConfigurationOverridesRoundTripDeterministically(t *testing.T) {
	overrides := []resources.ServiceConfigurationOverride{
		{Service: "users/store", Name: "postgres", Key: "POSTGRES_USER", Value: "mind"},
		{Service: "coordination/work-coordinator", Name: "mutation-permit", Key: "ED25519_SEED_BASE64", Value: "secret", Secret: true},
	}

	encoded, err := resources.EncodeServiceConfigurationOverrides(overrides)
	require.NoError(t, err)
	decoded, err := resources.DecodeServiceConfigurationOverrides(encoded)
	require.NoError(t, err)
	require.Equal(t, []resources.ServiceConfigurationOverride{
		{Service: "coordination/work-coordinator", Name: "mutation-permit", Key: "ED25519_SEED_BASE64", Value: "secret", Secret: true},
		{Service: "users/store", Name: "postgres", Key: "POSTGRES_USER", Value: "mind"},
	}, decoded)
}

func TestServiceConfigurationOverridesRequireUniqueQualifiedCoordinates(t *testing.T) {
	tests := []struct {
		name      string
		overrides []resources.ServiceConfigurationOverride
		want      string
	}{
		{
			name: "unqualified service",
			overrides: []resources.ServiceConfigurationOverride{
				{Service: "store", Name: "postgres", Key: "POSTGRES_USER", Value: "mind"},
			},
			want: "requires a module/service target",
		},
		{
			name: "normalized duplicate",
			overrides: []resources.ServiceConfigurationOverride{
				{Service: "coordination/work-coordinator", Name: "mutation-permit", Key: "ED25519-SEED-BASE64", Value: "first", Secret: true},
				{Service: "coordination/work_coordinator", Name: "mutation_permit", Key: "ed25519_seed_base64", Value: "second", Secret: true},
			},
			want: "duplicate service configuration override",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resources.EncodeServiceConfigurationOverrides(tt.overrides)
			require.ErrorContains(t, err, tt.want)
		})
	}
}
