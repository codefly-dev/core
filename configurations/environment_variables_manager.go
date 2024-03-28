package configurations

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type EnvironmentVariableManager struct {
	runtimeScope basev0.RuntimeScope

	environment *basev0.Environment

	configurations []*basev0.Configuration

	endpoints []*EndpointAccess

	restRoutes []*RestRouteAccess
}

func NewEnvironmentVariableManager() *EnvironmentVariableManager {
	return &EnvironmentVariableManager{}
}

func (holder *EnvironmentVariableManager) SetEnvironment(environment *basev0.Environment) {
	holder.environment = environment

}

func (holder *EnvironmentVariableManager) SetRuntimeScope(scope basev0.RuntimeScope) {
	holder.runtimeScope = scope
}

func (holder *EnvironmentVariableManager) Get() []string {
	var envs []string
	if holder.environment != nil {
		envs = append(envs, EnvironmentAsEnvironmentVariable(holder.environment))
	}
	confs := FindConfigurations(holder.configurations, holder.runtimeScope)
	for _, conf := range confs {
		envs = append(envs, ConfigurationAsEnvironmentVariables(conf)...)
	}
	for _, endpoint := range holder.endpoints {
		envs = append(envs, EndpointAsEnvironmentVariable(endpoint.Endpoint, endpoint.NetworkInstance))
	}
	for _, restRoute := range holder.restRoutes {
		envs = append(envs, RestRoutesAsEnvironmentVariable(restRoute.endpoint, restRoute.route))
	}
	return envs
}

func (holder *EnvironmentVariableManager) AddConfigurations(configurations ...*basev0.Configuration) error {
	holder.configurations = append(holder.configurations, configurations...)
	return nil
}

type EndpointAccess struct {
	*basev0.Endpoint
	*basev0.NetworkInstance
}

func (holder *EnvironmentVariableManager) AddPublicEndpoints(ctx context.Context, mappings []*basev0.NetworkMapping) error {
	for _, mp := range mappings {
		for _, instance := range mp.Instances {
			if instance.Scope == basev0.RuntimeScope_Public {
				holder.endpoints = append(holder.endpoints, &EndpointAccess{
					Endpoint:        mp.Endpoint,
					NetworkInstance: instance,
				})
			}
		}
	}
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
			if instance.Scope == basev0.RuntimeScope_Public {
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

const EnvironmentPrefix = "CODEFLY_ENVIRONMENT"

func EnvironmentAsEnvironmentVariable(env *basev0.Environment) string {
	return fmt.Sprintf("%s=%s", EnvironmentPrefix, env.Name)
}

func IsLocal(environment *basev0.Environment) bool {
	return environment.Name == "local"
}

const EndpointPrefix = "CODEFLY__ENDPOINT"

func EndpointAsEnvironmentVariableKey(endpoint *EndpointInformation) string {
	return fmt.Sprintf("%s__%s__%s__%s", endpoint.Application, endpoint.Service, endpoint.Name, endpoint.API)
}

func EndpointAsEnvironmentVariable(endpoint *basev0.Endpoint, instance *basev0.NetworkInstance) string {
	value := EncodeValue(instance.Address)
	env := fmt.Sprintf("%s_%s=%s", EnvironmentPrefix, EndpointAsEnvironmentVariableKey(EndpointInformationFromProto(endpoint)), value)
	return strings.ToUpper(env)
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
