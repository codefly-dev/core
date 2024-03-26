package configurations

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

const ConfigurationProjectOrigin = "_project.configuration"

const ProjectConfigurationPrefix = "CODEFLY__PROJECT_CONFIGURATION"
const ProjectSecretConfigurationPrefix = "CODEFLY__PROJECT_SECRET_CONFIGURATION"
const ServiceConfigurationPrefix = "CODEFLY__SERVICE_CONFIGURATION"
const SecretSecretConfigurationPrefix = "CODEFLY__SERVICE_SECRET_CONFIGURATION"

func GetConfigurationValue(_ context.Context, configuration *basev0.Configuration, name string, key string) (string, error) {
	for _, info := range configuration.Configurations {
		if info.Name == name {
			for _, value := range info.ConfigurationValues {
				if value.Key == key {
					return value.Value, nil
				}
			}
		}
	}
	return "", fmt.Errorf("cannot find configuration value: %s", key)
}

func FindConfigurations(configurations []*basev0.Configuration, scope basev0.RuntimeScope) []*basev0.Configuration {
	var found []*basev0.Configuration
	for _, conf := range configurations {
		if conf.Scope == scope {
			found = append(found, conf)
		}
	}
	return found
}

func ManyConfigurationAsEnvironmentVariables(confs ...*basev0.Configuration) []string {
	var envs []string
	for _, conf := range confs {
		envs = append(envs, ConfigurationAsEnvironmentVariables(conf)...)
	}
	return envs
}

// EncodeValue base64 encode
func EncodeValue(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func ConfigurationAsEnvironmentVariables(conf *basev0.Configuration) []string {
	var env []string
	confKey := ConfigurationEnvironmentKeyPrefix(conf)
	for _, info := range conf.Configurations {
		infoKey := fmt.Sprintf("%s__%s", confKey, NameToKey(info.Name))
		for _, value := range info.ConfigurationValues {
			key := fmt.Sprintf("%s__%s", infoKey, NameToKey(value.Key))
			if value.Secret {
				key = strings.Replace(key, "_CONFIGURATION__", "_SECRET_CONFIGURATION__", 1)
			}
			env = append(env, fmt.Sprintf("%s=%s", key, EncodeValue(value.Value)))
		}
	}
	return env
}

func NameToKey(name string) string {
	return strings.ToUpper(name)
}

func ConfigurationEnvironmentKeyPrefix(conf *basev0.Configuration) string {
	if conf.Origin == ConfigurationProjectOrigin {
		return ProjectConfigurationPrefix
	}
	return fmt.Sprintf("%s__%s", ServiceConfigurationPrefix, UniqueToKey(conf.Origin))
}

func UniqueToKey(origin string) string {
	origin = strings.Replace(origin, "/", "__", 1)
	origin = strings.Replace(origin, "-", "_", -1)
	return strings.ToUpper(origin)
}

func ConfigurationInformationHash(info *basev0.ConfigurationInformation) string {
	return HashString(info.String())
}

func ConfigurationInformationsHash(infos ...*basev0.ConfigurationInformation) (string, error) {
	hasher := NewHasher()
	for _, info := range infos {
		hasher.Add(ConfigurationInformationHash(info))
	}
	return hasher.Hash(), nil
}

func MakeManyConfigurationSummary(confs []*basev0.Configuration) string {
	var summary []string
	for _, conf := range confs {
		summary = append(summary, MakeConfigurationSummary(conf))
	}
	return strings.Join(summary, ", ")
}

func MakeConfigurationSummary(conf *basev0.Configuration) string {
	if conf == nil {
		return ""
	}
	var summary []string
	for _, c := range conf.Configurations {
		summary = append(summary, MakeConfigurationInformationSummary(c))
	}
	return fmt.Sprintf("%s: %s", conf.Origin, strings.Join(summary, ", "))

}

func MakeConfigurationInformationSummary(info *basev0.ConfigurationInformation) string {
	var summary []string
	for _, value := range info.ConfigurationValues {
		summary = append(summary, MakeConfigurationValueSummary(value))
	}
	return fmt.Sprintf("%s->%s", info.Name, strings.Join(summary, ", "))
}

func MakeConfigurationValueSummary(value *basev0.ConfigurationValue) string {
	if value.Secret {
		return fmt.Sprintf("%s=****", value.Key)
	}
	return fmt.Sprintf("%s=%s", value.Key, value.Value)
}
