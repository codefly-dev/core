package configurations_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/configurations"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

func TestConfigurationSerialization(t *testing.T) {

	// Assuming you have your ConfigurationValues in 'info.ConfigurationValues'
	info := &basev0.ConfigurationInformation{
		ConfigurationValues: []*basev0.ConfigurationValue{
			{Key: "server.host", Value: "localhost"},
			{Key: "server.port", Value: "8080"},
			{Key: "database.name", Value: "mydb"},
			{Key: "database.credentials.username", Value: "admin"},
			{Key: "database.credentials.password", Value: "secret"},
		},
	}

	type Config struct {
		Server struct {
			Host string `yaml:"host"`
			Port string `yaml:"port"`
		} `yaml:"server"`
		Database struct {
			Name        string `yaml:"name"`
			Credentials struct {
				Username string `yaml:"username"`
				Password string `yaml:"password"`
			} `yaml:"credentials"`
		} `yaml:"database"`
	}

	var config Config
	err := configurations.InformationUnmarshal(info, &config)
	require.NoError(t, err)

	require.Equal(t, "localhost", config.Server.Host)
	require.Equal(t, "admin", config.Database.Credentials.Username)
}
