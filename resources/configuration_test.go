package resources_test

import (
	"fmt"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

var Conf = &basev0.Configuration{
	Origin: resources.ConfigurationWorkspace,
	Configurations: []*basev0.ConfigurationInformation{
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
	Configurations: []*basev0.ConfigurationInformation{
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
	envs := resources.ConfigurationAsEnvironmentVariables(Conf, false)
	require.Len(t, envs, 1)
	needs := fmt.Sprintf("CODEFLY__WORKSPACE_CONFIGURATION__SOMETHING__GLOBAL=%s", "true")
	require.Contains(t, resources.EnvironmentVariableAsStrings(envs), needs)
}

func TestServiceConfigurationsAsEnvironmentVariables(t *testing.T) {
	envs := resources.ConfigurationAsEnvironmentVariables(serviceConf, true)
	require.Len(t, envs, 1)
	envs = append(envs, resources.ConfigurationAsEnvironmentVariables(serviceConf, false)...)
	needs := []string{
		fmt.Sprintf("CODEFLY__SERVICE_CONFIGURATION__APP__SVC__CONNECTION__URL=%s", "http://localhost:8080"),
		fmt.Sprintf("CODEFLY__SERVICE_SECRET_CONFIGURATION__APP__SVC__CONNECTION__PASSWORD=%s", "admin"),
	}
	require.ElementsMatch(t, resources.EnvironmentVariableAsStrings(envs), needs)
}
