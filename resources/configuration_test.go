package resources_test

import (
	"fmt"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

var Conf = &basev0.Configuration{
	Origin: resources.ConfigurationWorkspace,
	Infos: []*basev0.ConfigurationInformation{
		{
			Name: "something",
			ConfigurationValues: []*basev0.ConfigurationValue{
				{
					Key:   "global",
					Value: "true",
				},
			},
		},
	},
}

var serviceConf = &basev0.Configuration{
	Origin: "app/svc",
	Infos: []*basev0.ConfigurationInformation{
		{
			Name: "connection",
			ConfigurationValues: []*basev0.ConfigurationValue{
				{
					Key:   "url",
					Value: "http://localhost:8080",
				},
				{
					Key:    "password",
					Value:  "admin",
					Secret: true,
				},
			},
		},
	},
}

func TestConfigurationEnvironmentVariableKey(t *testing.T) {
	key := resources.ConfigurationEnvironmentKeyPrefix(Conf)
	require.Equal(t, "CODEFLY__WORKSPACE_CONFIGURATION", key)

	key = resources.ConfigurationEnvironmentKeyPrefix(serviceConf)
	require.Equal(t, "CODEFLY__SERVICE_CONFIGURATION__APP__SVC", key)
}

func TestConfigurationsAsEnvironmentVariables(t *testing.T) {
	envs, err := resources.ConfigurationAsEnvironmentVariables(Conf, "local", false)
	require.NoError(t, err)
	require.Len(t, envs, 1)
	needs := fmt.Sprintf("CODEFLY__WORKSPACE_CONFIGURATION__SOMETHING__GLOBAL=%s", "true")
	require.Contains(t, resources.EnvironmentVariableAsStrings(envs), needs)
}

func TestServiceConfigurationsAsEnvironmentVariables(t *testing.T) {
	envs, err := resources.ConfigurationAsEnvironmentVariables(serviceConf, "local", true)
	require.NoError(t, err)
	require.Len(t, envs, 1)
	public, err := resources.ConfigurationAsEnvironmentVariables(serviceConf, "local", false)
	require.NoError(t, err)
	envs = append(envs, public...)
	needs := []string{
		fmt.Sprintf("CODEFLY__SERVICE_CONFIGURATION__APP__SVC__CONNECTION__URL=%s", "http://localhost:8080"),
		fmt.Sprintf("CODEFLY__SERVICE_SECRET_CONFIGURATION__APP__SVC__CONNECTION__PASSWORD=%s", "admin"),
	}
	require.ElementsMatch(t, resources.EnvironmentVariableAsStrings(envs), needs)
}

func TestConfigurationEnvironmentKeysNormalizeHyphens(t *testing.T) {
	conf := &basev0.Configuration{
		Origin: "coordination/work-coordinator",
		Infos: []*basev0.ConfigurationInformation{{
			Name: "mutation-permit",
			ConfigurationValues: []*basev0.ConfigurationValue{{
				Key: "ed25519-seed-base64", Value: "redacted", Secret: true,
			}},
		}},
	}

	emitted, err := resources.ConfigurationAsEnvironmentVariables(conf, "local", true)
	require.NoError(t, err)
	require.Equal(t,
		"CODEFLY__SERVICE_SECRET_CONFIGURATION__COORDINATION__WORK_COORDINATOR__MUTATION_PERMIT__ED25519_SEED_BASE64=redacted",
		requireSingleEnvironmentVariable(t, emitted),
	)
	require.Equal(t,
		"CODEFLY__SERVICE_SECRET_CONFIGURATION__COORDINATION__WORK_COORDINATOR__MUTATION_PERMIT__ED25519_SEED_BASE64",
		resources.ServiceSecretConfigurationKeyFromUnique("coordination/work-coordinator", "mutation-permit", "ed25519-seed-base64"),
	)
}

func requireSingleEnvironmentVariable(t *testing.T, envs []*resources.EnvironmentVariable) string {
	t.Helper()
	require.Len(t, envs, 1)
	return envs[0].String()
}
