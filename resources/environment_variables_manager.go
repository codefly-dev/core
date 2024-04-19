package resources

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/wool"
)

type EnvironmentVariable struct {
	Key   string
	Value any
}

func (v EnvironmentVariable) String() string {
	return fmt.Sprintf("%s=%v", v.Key, v.Value)
}

func (v EnvironmentVariable) ValueAsString() string {
	return fmt.Sprintf("%v", v.Value)
}

func (v EnvironmentVariable) ValueAsEncodedString() string {
	return base64.StdEncoding.EncodeToString([]byte(v.ValueAsString()))
}

func EnvironmentVariableAsStrings(envs []EnvironmentVariable) []string {
	var result []string
	for _, env := range envs {
		result = append(result, env.String())
	}
	return result
}

type EnvironmentVariableManager struct {
	// Environment
	environment *basev0.Environment

	// How we are running
	runtimeContext *basev0.RuntimeContext

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

const RunningPrefix = "CODEFLY__RUNNING"

func Env(key string, value any) EnvironmentVariable {
	return EnvironmentVariable{
		Key:   key,
		Value: value,
	}
}

func (holder *EnvironmentVariableManager) SetRuntimeContext(runtimeContext *basev0.RuntimeContext) {
	holder.runtimeContext = runtimeContext
}

func (holder *EnvironmentVariableManager) getBase() []EnvironmentVariable {
	var envs []EnvironmentVariable
	if holder.running {
		envs = append(envs, Env(RunningPrefix, true))

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

func (holder *EnvironmentVariableManager) All() []EnvironmentVariable {
	envs := holder.getBase()
	for _, conf := range holder.configurations {
		envs = append(envs, ConfigurationAsEnvironmentVariables(conf, false)...)
		envs = append(envs, ConfigurationAsEnvironmentVariables(conf, true)...)
	}
	return envs
}

func (holder *EnvironmentVariableManager) Configurations() []EnvironmentVariable {
	envs := holder.getBase()
	for _, conf := range holder.configurations {
		envs = append(envs, ConfigurationAsEnvironmentVariables(conf, false)...)
	}
	return envs
}

func (holder *EnvironmentVariableManager) Secrets() []EnvironmentVariable {
	var envs []EnvironmentVariable
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

func (holder *EnvironmentVariableManager) AddEndpoints(ctx context.Context, mappings []*basev0.NetworkMapping, networkAccess *basev0.NetworkAccess) error {
	w := wool.Get(ctx).In("configurations.EnvironmentVariableManager.AddEndpoints")
	for _, mp := range mappings {
		for _, instance := range mp.Instances {
			if instance.Access.Kind == networkAccess.Kind {
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

func FindNetworkInstanceInEnvironmentVariables(ctx context.Context, endpointInfo *EndpointInformation, envs []string) (*NetworkInstance, error) {
	w := wool.Get(ctx).In("configurations.EnvironmentVariableManager.FindNetworkInstance")
	// Create the env key
	key := EndpointAsEnvironmentVariableKey(endpointInfo)
	w.Trace("searching for network instance", wool.NameField(key))
	for _, env := range envs {
		if after, found := strings.CutPrefix(env, fmt.Sprintf("%s=", key)); found {
			return ParseAddress(after)
		}
	}
	return nil, w.NewError("no network instance found")
}

func FindValueInEnvironmentVariables(ctx context.Context, key string, envs []string) (string, error) {
	w := wool.Get(ctx).In("configurations.EnvironmentVariableManager.FindValueInEnvironmentVariables")
	for _, env := range envs {
		if after, found := strings.CutPrefix(env, fmt.Sprintf("%s=", key)); found {
			return after, nil
		}
	}
	return "", w.NewError("no value found")
}

type RestRouteAccess struct {
	endpoint *basev0.Endpoint
	route    *basev0.RestRoute
}

func (holder *EnvironmentVariableManager) AddRestRoutes(ctx context.Context, mappings []*basev0.NetworkMapping, networkAccess *basev0.NetworkAccess) error {
	w := wool.Get(ctx).In("configurations.EnvironmentVariableManager.AddRestRoutes")
	for _, mp := range mappings {
		rest := IsRest(ctx, mp.Endpoint)
		if rest == nil {
			continue
		}
		for _, instance := range mp.Instances {
			if instance.Access.Kind == networkAccess.Kind {
				for _, group := range rest.Groups {
					for _, route := range group.Routes {
						w.Debug("adding rest route",
							wool.NameField(route.Path),
							wool.ModuleField(mp.Endpoint.Module),
							wool.ServiceField(mp.Endpoint.Service))
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

func EnvironmentAsEnvironmentVariable(env *basev0.Environment) EnvironmentVariable {
	return Env(EnvironmentPrefix, env.Name)
}

func IsLocal(environment *basev0.Environment) bool {
	return environment.Name == "local"
}

const EndpointPrefix = "CODEFLY__ENDPOINT"

func EndpointAsEnvironmentVariableKeyBase(info *EndpointInformation) string {
	return strings.ToUpper(fmt.Sprintf("%s__%s__%s__%s", info.Module, info.Service, info.Name, info.API))
}

func EndpointAsEnvironmentVariableKey(info *EndpointInformation) string {
	return strings.ToUpper(fmt.Sprintf("%s__%s", EndpointPrefix, EndpointAsEnvironmentVariableKeyBase(info)))
}

func EndpointAsEnvironmentVariable(endpoint *basev0.Endpoint, instance *basev0.NetworkInstance) EnvironmentVariable {
	value := instance.Address
	key := EndpointAsEnvironmentVariableKey(EndpointInformationFromProto(endpoint))
	return Env(key, value)
}

// ConfigurationAsEnvironmentVariables converts a configuration to a list of environment variables
// the secret flag decides if we return secret or regular values
func ConfigurationAsEnvironmentVariables(conf *basev0.Configuration, secret bool) []EnvironmentVariable {
	var env []EnvironmentVariable
	confKey := ConfigurationEnvironmentKeyPrefix(conf)
	for _, info := range conf.Configurations {
		infoKey := fmt.Sprintf("%s__%s", confKey, NameToKey(info.Name))
		for _, value := range info.ConfigurationValues {
			key := fmt.Sprintf("%s__%s", infoKey, NameToKey(value.Key))
			// if secret: only add secret values
			if secret {
				if value.Secret {
					key = strings.Replace(key, "_CONFIGURATION__", "_SECRET_CONFIGURATION__", 1)
					env = append(env, Env(key, value.Value))
				}
			} else {
				if !value.Secret {
					env = append(env, Env(key, value.Value))
				}
			}
		}
	}
	return env
}

func ServiceConfigurationKeyFromUnique(unique string, name string, key string) string {
	k := fmt.Sprintf("%s__%s__%s", ServiceConfigurationEnvironmentKeyPrefixFromUnique(unique), name, key)
	return strings.ToUpper(k)
}

func ServiceConfigurationKey(service *Service, name string, key string) string {
	return ServiceConfigurationKeyFromUnique(service.Unique(), name, key)
}

func ServiceSecretConfigurationKeyFromUnique(unique string, name string, key string) string {
	k := fmt.Sprintf("%s__%s__%s", ServiceSecretConfigurationEnvironmentKeyPrefixFromUnique(unique), name, key)
	return strings.ToUpper(k)
}

func ServiceSecretConfigurationKey(service *Service, name string, key string) string {
	return ServiceSecretConfigurationKeyFromUnique(service.Unique(), name, key)
}

func NameToKey(name string) string {
	return strings.ToUpper(name)
}

func ConfigurationEnvironmentKeyPrefix(conf *basev0.Configuration) string {
	if conf.Origin == ConfigurationOrigin {
		return ConfigurationPrefix
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

func RestRoutesAsEnvironmentVariable(endpoint *basev0.Endpoint, route *basev0.RestRoute) EnvironmentVariable {
	return Env(RestRouteEnvironmentVariableKey(EndpointInformationFromProto(endpoint), route), endpoint.Visibility)
}

func RestRouteEnvironmentVariableKey(info *EndpointInformation, route *basev0.RestRoute) string {
	key := EndpointAsEnvironmentVariableKeyBase(info)
	// Add path
	key = fmt.Sprintf("%s__%s", RestRoutePrefix, key)
	key = fmt.Sprintf("%s___%s", key, sanitizePath(route.Path))
	key = fmt.Sprintf("%s___%s", key, ConvertHTTPMethodFromProto(route.Method))
	return strings.ToUpper(key)
}
