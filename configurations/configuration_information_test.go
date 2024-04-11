package configurations_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

var projectConf = &basev0.Configuration{
	Origin: configurations.ConfigurationProjectOrigin,
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
	key := configurations.ConfigurationEnvironmentKeyPrefix(projectConf)
	assert.Equal(t, "CODEFLY__PROJECT_CONFIGURATION", key)

	key = configurations.ConfigurationEnvironmentKeyPrefix(serviceConf)
	assert.Equal(t, "CODEFLY__SERVICE_CONFIGURATION__APP__SVC", key)
}

func TestProjectConfigurationsAsEnvironmentVariables(t *testing.T) {
	envs := configurations.ConfigurationAsEnvironmentVariables(projectConf, false)
	assert.Len(t, envs, 1)
	needs := fmt.Sprintf("CODEFLY__PROJECT_CONFIGURATION__SOMETHING__GLOBAL=%s", "true")
	assert.Contains(t, configurations.EnvironmentVariableAsStrings(envs), needs)
}

func TestServiceConfigurationsAsEnvironmentVariables(t *testing.T) {
	envs := configurations.ConfigurationAsEnvironmentVariables(serviceConf, true)
	assert.Len(t, envs, 1)
	envs = append(envs, configurations.ConfigurationAsEnvironmentVariables(serviceConf, false)...)
	needs := []string{
		fmt.Sprintf("CODEFLY__SERVICE_CONFIGURATION__APP__SVC__CONNECTION__URL=%s", "http://localhost:8080"),
		fmt.Sprintf("CODEFLY__SERVICE_SECRET_CONFIGURATION__APP__SVC__CONNECTION__PASSWORD=%s", "admin"),
	}
	assert.ElementsMatch(t, configurations.EnvironmentVariableAsStrings(envs), needs)
}
