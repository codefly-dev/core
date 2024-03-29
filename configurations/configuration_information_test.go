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
	env := configurations.ConfigurationAsEnvironmentVariables(projectConf, false)
	assert.Len(t, env, 1)
	needs := fmt.Sprintf("CODEFLY__PROJECT_CONFIGURATION__SOMETHING__GLOBAL=%s", configurations.EncodeValue("true"))
	assert.Contains(t, env, needs)
}

func TestServiceConfigurationsAsEnvironmentVariables(t *testing.T) {
	env := configurations.ConfigurationAsEnvironmentVariables(serviceConf, true)
	assert.Len(t, env, 1)
	env = append(env, configurations.ConfigurationAsEnvironmentVariables(serviceConf, false)...)
	needs := []string{
		fmt.Sprintf("CODEFLY__SERVICE_CONFIGURATION__APP__SVC__CONNECTION__URL=%s", configurations.EncodeValue("http://localhost:8080")),
		fmt.Sprintf("CODEFLY__SERVICE_SECRET_CONFIGURATION__APP__SVC__CONNECTION__PASSWORD=%s", configurations.EncodeValue("admin")),
	}
	for _, need := range needs {
		assert.Contains(t, env, need)
	}
}
