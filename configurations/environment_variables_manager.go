package configurations

import (
	"context"
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/wool"
)

type EnvironmentVariableManager struct {
	networkScope basev0.NetworkScope

	environment *basev0.Environment

	configurations []*basev0.Configuration

	endpoints []*EndpointAccess

	restRoutes []*RestRouteAccess
	running    bool
}

func NewEnvironmentVariableManager() *EnvironmentVariableManager {
	return &EnvironmentVariableManager{}
}

func (holder *EnvironmentVariableManager) SetEnvironment(environment *basev0.Environment) {
	holder.environment = environment

}

func (holder *EnvironmentVariableManager) SetNetworkScope(scope basev0.NetworkScope) {
	holder.networkScope = scope
}

func (holder *EnvironmentVariableManager) getBase() []string {
	var envs []string
	if holder.running {
		envs = append(envs, "CODEFLY__RUNNING=true")

	}
	if holder.environment != nil {
		envs = append(envs, EnvironmentAsEnvironmentVariable(holder.environment))
	}

	for _, endpoint := range holder.endpoints {
		envs = append(envs, EndpointAsEnvironmentVariable(endpoint.Endpoint, endpoint.NetworkInstance))
	}
	for _, restRoute := range holder.restRoutes {
		envs = append(envs, RestRoutesAsEnvironmentVariable(restRoute.endpoint, restRoute.route))
	}
	return envs
}

func (holder *EnvironmentVariableManager) All() []string {
	envs := holder.getBase()
	for _, conf := range holder.configurations {
		envs = append(envs, ConfigurationAsEnvironmentVariables(conf, false)...)
		envs = append(envs, ConfigurationAsEnvironmentVariables(conf, true)...)
	}
	return envs
}

func (holder *EnvironmentVariableManager) Configurations() []string {
	envs := holder.getBase()
	for _, conf := range holder.configurations {
		envs = append(envs, ConfigurationAsEnvironmentVariables(conf, false)...)
	}
	return envs
}

func (holder *EnvironmentVariableManager) Secrets() []string {
	var envs []string
	for _, conf := range holder.configurations {
		envs = append(envs, ConfigurationAsEnvironmentVariables(conf, true)...)
	}
	return envs
}

func (holder *EnvironmentVariableManager) AddConfigurations(configurations ...*basev0.Configuration) error {
	for _, conf := range configurations {
		if conf != nil {
			holder.configurations = append(holder.configurations, conf)
		}
	}
	return nil
}

type EndpointAccess struct {
	*basev0.Endpoint
	*basev0.NetworkInstance
}

func (holder *EnvironmentVariableManager) AddPublicEndpoints(ctx context.Context, mappings []*basev0.NetworkMapping) error {
	w := wool.Get(ctx).In("configurations.EnvironmentVariableManager.AddPublicEndpoints")
	for _, mp := range mappings {
		for _, instance := range mp.Instances {
			if instance.Scope == basev0.NetworkScope_Public {
				holder.endpoints = append(holder.endpoints, &EndpointAccess{
					Endpoint:        mp.Endpoint,
					NetworkInstance: instance,
				})
			}
		}
	}
	w.Debug("added # public endpoints", wool.SliceCountField(holder.endpoints))
	return nil
}

type RestRouteAccess struct {
	endpoint *basev0.Endpoint
	route    *basev0.RestRoute
}

func (holder *EnvironmentVariableManager) AddPublicRestRoutes(ctx context.Context, mappings []*basev0.NetworkMapping) error {
	for _, mp := range mappings {
		rest := IsRest(ctx, mp.Endpoint)
		if rest == nil {
			continue
		}
		for _, instance := range mp.Instances {
			if instance.Scope == basev0.NetworkScope_Public {
				for _, group := range rest.Groups {
					for _, route := range group.Routes {
						holder.restRoutes = append(holder.restRoutes, &RestRouteAccess{
							route:    route,
							endpoint: mp.Endpoint,
						})
					}
				}
			}
		}
	}
	return nil

}

func (holder *EnvironmentVariableManager) SetRunning(b bool) {
	holder.running = b

}

const EnvironmentPrefix = "CODEFLY_ENVIRONMENT"

func EnvironmentAsEnvironmentVariable(env *basev0.Environment) string {
	return fmt.Sprintf("%s=%s", EnvironmentPrefix, env.Name)
}

func IsLocal(environment *basev0.Environment) bool {
	return environment.Name == "local"
}

const EndpointPrefix = "CODEFLY__ENDPOINT"

func EndpointAsEnvironmentVariableKey(endpoint *EndpointInformation) string {
	return strings.ToUpper(fmt.Sprintf("%s__%s__%s__%s", endpoint.Application, endpoint.Service, endpoint.Name, endpoint.API))
}

func EndpointAsEnvironmentVariable(endpoint *basev0.Endpoint, instance *basev0.NetworkInstance) string {
	value := instance.Address
	env := fmt.Sprintf("%s__%s=%s", EndpointPrefix, EndpointAsEnvironmentVariableKey(EndpointInformationFromProto(endpoint)), value)
	return env
}

const ConfigurationPrefix = "CODEFLY__CONFIGURATION"

// ConfigurationAsEnvironmentVariables converts a configuration to a list of environment variables
// the secret flag decides if we return secret or regular values
func ConfigurationAsEnvironmentVariables(conf *basev0.Configuration, secret bool) []string {
	var env []string
	confKey := ConfigurationEnvironmentKeyPrefix(conf)
	for _, info := range conf.Configurations {
		infoKey := fmt.Sprintf("%s__%s", confKey, NameToKey(info.Name))
		for _, value := range info.ConfigurationValues {
			key := fmt.Sprintf("%s__%s", infoKey, NameToKey(value.Key))
			// if secret: only add secret values
			if secret {
				if value.Secret {
					key = strings.Replace(key, "_CONFIGURATION__", "_SECRET_CONFIGURATION__", 1)
					env = append(env, fmt.Sprintf("%s=%s", key, value.Value))
				}
			} else {
				if !value.Secret {
					env = append(env, fmt.Sprintf("%s=%s", key, value.Value))
				}
			}
		}
	}
	return env
}

func ServiceConfigurationKey(service *Service, name string, key string) string {
	k := fmt.Sprintf("%s__%s__%s", ServiceConfigurationEnvironmentKeyPrefixFromUnique(service.Unique()), name, key)
	return strings.ToUpper(k)
}

func ServiceSecretConfigurationKey(service *Service, name string, key string) string {
	k := fmt.Sprintf("%s__%s__%s", ServiceSecretConfigurationEnvironmentKeyPrefixFromUnique(service.Unique()), name, key)
	return strings.ToUpper(k)
}

func NameToKey(name string) string {
	return strings.ToUpper(name)
}

func ConfigurationEnvironmentKeyPrefix(conf *basev0.Configuration) string {
	if conf.Origin == ConfigurationProjectOrigin {
		return ProjectConfigurationPrefix
	}
	return ServiceConfigurationEnvironmentKeyPrefixFromUnique(conf.Origin)
}

func ServiceConfigurationEnvironmentKeyPrefixFromUnique(unique string) string {
	return fmt.Sprintf("%s__%s", ServiceConfigurationPrefix, UniqueToKey(unique))
}

func ServiceSecretConfigurationEnvironmentKeyPrefixFromUnique(unique string) string {
	return fmt.Sprintf("%s__%s", ServiceSecretConfigurationPrefix, UniqueToKey(unique))
}

func UniqueToKey(origin string) string {
	origin = strings.ReplaceAll(origin, "/", "__")
	origin = strings.ReplaceAll(origin, "-", "_")
	return strings.ToUpper(origin)
}

const RestRoutePrefix = "CODEFLY__REST_ROUTE"

func RestRoutesAsEnvironmentVariable(endpoint *basev0.Endpoint, route *basev0.RestRoute) string {
	return fmt.Sprintf("%s=%s", RestRouteEnvironmentVariableKey(EndpointInformationFromProto(endpoint), route), endpoint.Visibility)
}

func RestRouteEnvironmentVariableKey(endpoint *EndpointInformation, route *basev0.RestRoute) string {
	key := EndpointAsEnvironmentVariableKey(endpoint)
	// Add path
	key = fmt.Sprintf("%s__%s", RestRoutePrefix, key)
	key = fmt.Sprintf("%s___%s", key, sanitizePath(route.Path))
	key = fmt.Sprintf("%s___%s", key, ConvertHTTPMethodFromProto(route.Method))
	return strings.ToUpper(key)
}
