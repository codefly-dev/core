package resources

import (
	"context"
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/wool"
)

const ConfigurationWorkspace = "workspace"

func HasConfigurationInformation(_ context.Context, conf *basev0.Configuration, name string) bool {
	for _, info := range conf.Configurations {
		if info.Name == name {
			return true
		}
	}
	return false
}

func FindServiceConfiguration(_ context.Context, confs []*basev0.Configuration, runtimeContext *basev0.RuntimeContext, unique string) (*basev0.Configuration, error) {
	for _, conf := range confs {
		if conf.RuntimeContext.Kind == runtimeContext.Kind && conf.Origin == unique {
			return conf, nil
		}
	}
	return nil, fmt.Errorf("couldn't find service configuration: %s", unique)
}

func GetConfigurationValue(ctx context.Context, conf *basev0.Configuration, name string, key string) (string, error) {
	w := wool.Get(ctx).In("GetConfigurationValue")
	if conf == nil {
		return "", w.NewError("configuration is nil")
	}
	for _, info := range conf.Configurations {
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

func FindConfigurations(configurations []*basev0.Configuration, runtime *basev0.RuntimeContext) []*basev0.Configuration {
	var found []*basev0.Configuration
	for _, conf := range configurations {
		if conf.RuntimeContext.Kind == runtime.Kind {
			found = append(found, conf)
		}
	}
	return found
}

func ConfigurationsHash(confs ...*basev0.Configuration) string {
	hasher := NewHasher()
	for _, conf := range confs {
		hasher.Add(ConfigurationHash(conf))
	}
	return hasher.Hash()
}

func ConfigurationHash(conf *basev0.Configuration) string {
	hasher := NewHasher()
	for _, info := range conf.Configurations {
		hasher.Add(ConfigurationInformationHash(info))
	}
	return hasher.Hash()
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

func FilterConfigurations(configurations []*basev0.Configuration, runtimeContext *basev0.RuntimeContext) []*basev0.Configuration {
	var out []*basev0.Configuration
	for _, conf := range configurations {
		if conf.RuntimeContext.Kind == runtimeContext.Kind {
			out = append(out, conf)
		}
	}
	return out
}

func ExtractConfiguration(configurations []*basev0.Configuration, runtimeContext *basev0.RuntimeContext) (*basev0.Configuration, error) {
	var out *basev0.Configuration
	for _, conf := range configurations {
		if conf.RuntimeContext.Kind == runtimeContext.Kind {
			if out != nil {
				return nil, fmt.Errorf("multiple configurations found for runtime context: %s", runtimeContext.Kind)
			}
			out = conf
		}
	}
	return out, nil
}

func FindConfigurationValue(conf *basev0.Configuration, name string, key string) (string, error) {
	for _, info := range conf.Configurations {
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
