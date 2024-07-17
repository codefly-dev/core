package resources

import (
	"context"
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/wool"
)

const ConfigurationWorkspace = "_workspace_origin"

func HasConfigurationInformation(_ context.Context, conf *basev0.Configuration, name string) bool {
	for _, info := range conf.Infos {
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
	for _, info := range conf.Infos {
		if info.Name == name {
			for _, value := range info.ConfigurationValues {
				if value.Key == key {
					return value.Value, nil
				}
			}
		}
	}
	return "", nil
}

func GetConfigurationInformation(ctx context.Context, conf *basev0.Configuration, name string) (*basev0.ConfigurationInformation, error) {
	w := wool.Get(ctx).In("GetConfigurationValue")
	if conf == nil {
		return nil, w.NewError("configuration is nil")
	}
	return FilterConfigurationInformation(ctx, name, conf.Infos...)
}

func FilterConfigurationInformation(_ context.Context, name string, infos ...*basev0.ConfigurationInformation) (*basev0.ConfigurationInformation, error) {
	for _, info := range infos {
		if info.Name == name {
			return info, nil
		}
	}
	return nil, nil
}

func FindWorkspaceConfiguration(_ context.Context, confs []*basev0.Configuration, name string) (*basev0.Configuration, error) {
	for _, conf := range confs {
		if conf.Origin != ConfigurationWorkspace {
			continue
		}
		if conf.Infos[0].Name == name {
			return conf, nil
		}
	}
	return nil, fmt.Errorf("couldn't find workspace configuration: %s", name)
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
	for _, info := range conf.Infos {
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
	for _, info := range conf.Infos {
		summary = append(summary, MakeConfigurationInformationSummary(info))
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
