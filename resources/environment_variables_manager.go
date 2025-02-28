package resources

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
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

func EnvironmentVariableAsStrings(envs []*EnvironmentVariable) []string {
	var result []string
	for _, env := range envs {
		result = append(result, env.String())
	}
	return result
}

type EnvironmentVariableManager struct {
	// Environment
	environment *basev0.Environment

	// What we are running
	workspace string
	module    string
	service   string
	version   string

	// How we are running
	runtimeContext *basev0.RuntimeContext

	configurations    []*basev0.Configuration
	rawConfigurations []*basev0.Configuration

	endpoints []*EndpointAccess

	restRoutes []*RestRouteAccess
	running    bool
	fixture    string

	// Other environment variables
	others []*EnvironmentVariable
}

func NewEnvironmentVariableManager() *EnvironmentVariableManager {
	return &EnvironmentVariableManager{}
}

func (holder *EnvironmentVariableManager) SetEnvironment(environment *basev0.Environment) {
	holder.environment = environment
}

func Env(key string, value any) *EnvironmentVariable {
	return &EnvironmentVariable{
		Key:   key,
		Value: value,
	}
}

func (holder *EnvironmentVariableManager) getBase() []*EnvironmentVariable {
	var envs []*EnvironmentVariable

	if holder.running {
		envs = append(envs, Env(RunningPrefix, true))
	}

	if holder.fixture != "" {
		envs = append(envs, FixtureAsEnvironmentVariable(holder.fixture))
	}

	if holder.environment != nil {
		envs = append(envs, EnvironmentAsEnvironmentVariable(holder.environment))
	}

	if holder.workspace != "" {
		envs = append(envs, WorkspaceAsEnvironmentVariable(holder.workspace))
	}

	if holder.module != "" {
		envs = append(envs, ModuleAsEnvironmentVariable(holder.module))
	}

	if holder.service != "" {
		envs = append(envs, ServiceAsEnvironmentVariable(holder.service))
	}

	if holder.version != "" {
		envs = append(envs, VersionAsEnvironmentVariable(holder.version))
	}

	if holder.runtimeContext != nil {
		envs = append(envs, RuntimeContextAsEnvironmentVariable(holder.runtimeContext))
	}

	for _, endpoint := range holder.endpoints {
		envs = append(envs, EndpointAsEnvironmentVariable(endpoint))
	}

	for _, restRoute := range holder.restRoutes {
		envs = append(envs, RestRoutesAsEnvironmentVariable(restRoute))
	}
	envs = append(envs, holder.others...)
	return envs
}

func (holder *EnvironmentVariableManager) Endpoints() []*EndpointAccess {
	return holder.endpoints
}

func (holder *EnvironmentVariableManager) All() []*EnvironmentVariable {
	envs := holder.getBase()
	for _, conf := range holder.configurations {
		envs = append(envs, ConfigurationAsEnvironmentVariables(conf, false)...)
		envs = append(envs, ConfigurationAsEnvironmentVariables(conf, true)...)
	}
	for _, conf := range holder.rawConfigurations {
		envs = append(envs, ConfigurationAsRawEnvironmentVariables(conf)...)
	}
	return envs
}

func (holder *EnvironmentVariableManager) SetIdentity(identity *basev0.ServiceIdentity) {
	holder.module = identity.Module
	holder.service = identity.Name
	holder.workspace = identity.Workspace
	holder.version = identity.Version
}

const RunningPrefix = "CODEFLY__RUNNING"

func (holder *EnvironmentVariableManager) SetRunning() {
	holder.running = true
}

const FixturePrefix = "CODEFLY__FIXTURE"

func (holder *EnvironmentVariableManager) SetFixture(fixture string) {
	holder.fixture = fixture
}

func FixtureAsEnvironmentVariable(fixture string) *EnvironmentVariable {
	return Env(FixturePrefix, fixture)
}

const WorkspacePrefix = "CODEFLY__WORKSPACE"

func WorkspaceAsEnvironmentVariable(workspace string) *EnvironmentVariable {
	return Env(WorkspacePrefix, workspace)
}

const ModulePrefix = "CODEFLY__MODULE"

func ModuleAsEnvironmentVariable(module string) *EnvironmentVariable {
	return Env(ModulePrefix, module)
}

const ServicePrefix = "CODEFLY__SERVICE"

func ServiceAsEnvironmentVariable(service string) *EnvironmentVariable {
	return Env(ServicePrefix, service)
}

const VersionPrefix = "CODEFLY__SERVICE_VERSION"

func VersionAsEnvironmentVariable(version string) *EnvironmentVariable {
	return Env(VersionPrefix, version)
}

const RuntimeContextPrefix = "CODEFLY__RUNTIME_CONTEXT"

func RuntimeContextAsEnvironmentVariable(runtimeContext *basev0.RuntimeContext) *EnvironmentVariable {
	return Env(RuntimeContextPrefix, runtimeContext.Kind)
}

func (holder *EnvironmentVariableManager) SetRuntimeContext(runtimeContext *basev0.RuntimeContext) {
	holder.runtimeContext = runtimeContext
}

const WorkspaceConfigurationPrefix = "CODEFLY__WORKSPACE_CONFIGURATION"

// #nosec G101
const WorkspaceSecretConfigurationPrefix = "CODEFLY__WORKSPACE_SECRET_CONFIGURATION"
const ServiceConfigurationPrefix = "CODEFLY__SERVICE_CONFIGURATION"
const ServiceSecretConfigurationPrefix = "CODEFLY__SERVICE_SECRET_CONFIGURATION"

func (holder *EnvironmentVariableManager) Configurations() []*EnvironmentVariable {
	envs := holder.getBase()
	for _, conf := range holder.configurations {
		envs = append(envs, ConfigurationAsEnvironmentVariables(conf, false)...)
	}
	return envs
}

func (holder *EnvironmentVariableManager) Secrets() []*EnvironmentVariable {
	var envs []*EnvironmentVariable
	for _, conf := range holder.configurations {
		envs = append(envs, ConfigurationAsEnvironmentVariables(conf, true)...)
	}
	return envs
}

func (holder *EnvironmentVariableManager) AddConfigurations(_ context.Context, configurations ...*basev0.Configuration) error {
	for _, conf := range configurations {
		if conf != nil {
			holder.configurations = append(holder.configurations, conf)
		}
	}
	return nil
}

func (holder *EnvironmentVariableManager) AddRawConfigurations(_ context.Context, configurations ...*basev0.Configuration) error {
	for _, conf := range configurations {
		if conf != nil {
			holder.rawConfigurations = append(holder.rawConfigurations, conf)
		}
	}
	return nil
}

type EndpointAccess struct {
	Endpoint        *basev0.Endpoint
	NetworkInstance *basev0.NetworkInstance
	prefix          string
}

func MakeManyEndpointAccessSummary(endpointAccesses []*EndpointAccess) string {
	var result []string
	for _, ea := range endpointAccesses {
		result = append(result, MakeEndpointAccessSummary(ea))
	}
	return strings.Join(result, ", ")
}

func MakeEndpointAccessSummary(endpointAccess *EndpointAccess) string {
	return fmt.Sprintf("%s::%s", MakeEndpointSummary(endpointAccess.Endpoint), MakeNetworkInstanceSummary(endpointAccess.NetworkInstance))
}

func MakeNetworkInstanceSummary(instance *basev0.NetworkInstance) string {
	return fmt.Sprintf("%s::%s", instance.Address, instance.Access.Kind)
}

type EnvironmentVariableOptions struct {
	publicPrefix    string
	nonPublicPrefix string
}

type EnvironmentVariableOption func(*EnvironmentVariableOptions)

func WithPublicEnvironmentVariablePrefix(prefix string) EnvironmentVariableOption {
	return func(options *EnvironmentVariableOptions) {
		options.publicPrefix = prefix
	}
}

func WithNonPublicEnvironmentVariablePrefix(prefix string) EnvironmentVariableOption {
	return func(options *EnvironmentVariableOptions) {
		options.nonPublicPrefix = prefix
	}
}

func prefix(nm *basev0.NetworkMapping, opt *EnvironmentVariableOptions) string {
	if nm.Endpoint.Visibility == VisibilityPublic {
		return opt.publicPrefix
	}
	return opt.nonPublicPrefix
}

func createEnvironmentVariableOptions(opts ...EnvironmentVariableOption) *EnvironmentVariableOptions {
	options := &EnvironmentVariableOptions{}
	for _, opt := range opts {
		opt(options)
	}
	return options
}

func (holder *EnvironmentVariableManager) AddEndpoints(ctx context.Context, mappings []*basev0.NetworkMapping, networkAccess *basev0.NetworkAccess, opts ...EnvironmentVariableOption) error {
	w := wool.Get(ctx).In("configurations.EnvironmentVariableManager.AddEndpoints")
	opt := createEnvironmentVariableOptions(opts...)
	for _, mp := range mappings {
		for _, instance := range mp.Instances {
			if instance.Access.Kind == networkAccess.Kind {
				holder.endpoints = append(holder.endpoints, &EndpointAccess{
					Endpoint:        mp.Endpoint,
					NetworkInstance: instance,
					prefix:          prefix(mp, opt),
				})
			}
		}
	}
	w.Debug("added endpoints", wool.SliceCountField(holder.endpoints))
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
	predix   string
}

func ExtractRestRoutes(ctx context.Context, mappings []*basev0.NetworkMapping, networkAccess *basev0.NetworkAccess, opts ...EnvironmentVariableOption) ([]*RestRouteAccess, error) {
	w := wool.Get(ctx).In("configurations.EnvironmentVariableManager.ExtractRestRoutes")
	opt := createEnvironmentVariableOptions(opts...)
	var result []*RestRouteAccess
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
						result = append(result, &RestRouteAccess{
							route:    route,
							endpoint: mp.Endpoint,
							predix:   prefix(mp, opt),
						})
					}
				}
			}
		}
	}
	w.Debug("got rest routes", wool.SliceCountField(result))
	return result, nil
}

func (holder *EnvironmentVariableManager) AddRestRoutes(ctx context.Context, mappings []*basev0.NetworkMapping, networkAccess *basev0.NetworkAccess, opts ...EnvironmentVariableOption) error {
	w := wool.Get(ctx).In("configurations.EnvironmentVariableManager.AddRestRoutes")
	routes, err := ExtractRestRoutes(ctx, mappings, networkAccess, opts...)
	if err != nil {
		return w.Wrapf(err, "failed to extract rest routes")
	}
	holder.restRoutes = append(holder.restRoutes, routes...)
	return nil
}

// AddEnvironmentVariable adds an environment variable to the manager
func (holder *EnvironmentVariableManager) AddEnvironmentVariable(ctx context.Context, key string, value string) {
	holder.others = append(holder.others, Env(key, value))
}

const EnvironmentPrefix = "CODEFLY__ENVIRONMENT"

func EnvironmentAsEnvironmentVariable(env *basev0.Environment) *EnvironmentVariable {
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

func EndpointAsEnvironmentVariable(endpointAccess *EndpointAccess) *EnvironmentVariable {
	info := EndpointInformationFromProto(endpointAccess.Endpoint)
	key := EndpointAsEnvironmentVariableKey(info)
	if endpointAccess.prefix != "" {
		key = fmt.Sprintf("%s%s", endpointAccess.prefix, key)
	}
	value := endpointAccess.NetworkInstance.Address
	return Env(key, value)
}

// ConfigurationAsEnvironmentVariables converts a configuration to a list of environment variables
// the secret flag decides if we return secret or regular values
func ConfigurationAsEnvironmentVariables(conf *basev0.Configuration, secret bool) []*EnvironmentVariable {
	var env []*EnvironmentVariable
	confKey := ConfigurationEnvironmentKeyPrefix(conf)
	for _, info := range conf.Infos {
		infoKey := fmt.Sprintf("%s__%s", confKey, NameToKey(info.Name))
		for _, value := range info.ConfigurationValues {
			key := fmt.Sprintf("%s__%s", infoKey, NameToKey(value.Key))
			// if secret: only add secret values
			if secret {
				if value.Secret {
					key = strings.Replace(key, WorkspaceConfigurationPrefix, WorkspaceSecretConfigurationPrefix, 1)
					key = strings.Replace(key, ServiceConfigurationPrefix, ServiceSecretConfigurationPrefix, 1)
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

// ConfigurationAsEnvironmentVariables converts a configuration to a list of environment variables
// the secret flag decides if we return secret or regular values
func ConfigurationAsRawEnvironmentVariables(conf *basev0.Configuration) []*EnvironmentVariable {
	var env []*EnvironmentVariable
	for _, info := range conf.Infos {
		for _, value := range info.ConfigurationValues {
			env = append(env, Env(value.Key, value.Value))
		}
	}
	return env
}

func ServiceConfigurationKeyFromUnique(unique string, name string, key string) string {
	k := fmt.Sprintf("%s__%s__%s", ServiceConfigurationEnvironmentKeyPrefixFromUnique(unique), name, key)
	return strings.ToUpper(k)
}

func ServiceConfigurationKey(service *ServiceIdentity, name string, key string) string {
	return ServiceConfigurationKeyFromUnique(service.Unique(), name, key)
}

func ServiceSecretConfigurationKeyFromUnique(unique string, name string, key string) string {
	k := fmt.Sprintf("%s__%s__%s", ServiceSecretConfigurationEnvironmentKeyPrefixFromUnique(unique), name, key)
	return strings.ToUpper(k)
}

func ServiceSecretConfigurationKey(service *ServiceIdentity, name string, key string) string {
	return ServiceSecretConfigurationKeyFromUnique(service.Unique(), name, key)
}

func NameToKey(name string) string {
	return strings.ToUpper(name)
}

func ConfigurationEnvironmentKeyPrefix(conf *basev0.Configuration) string {
	if conf.Origin == ConfigurationWorkspace {
		return WorkspaceConfigurationPrefix
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

func RestRoutesAsEnvironmentVariable(restRoute *RestRouteAccess) *EnvironmentVariable {
	key := RestRouteEnvironmentVariableKey(EndpointInformationFromProto(restRoute.endpoint), restRoute.route)
	if restRoute.predix != "" {
		key = fmt.Sprintf("%s%s", restRoute.predix, key)
	}
	return Env(key, restRoute.endpoint.Visibility)
}

func RestRouteEnvironmentVariableKey(info *EndpointInformation, route *basev0.RestRoute) string {
	key := EndpointAsEnvironmentVariableKeyBase(info)
	// Add path
	key = fmt.Sprintf("%s__%s", RestRoutePrefix, key)
	key = fmt.Sprintf("%s___%s", key, sanitizePath(route.Path))
	key = fmt.Sprintf("%s___%s", key, ConvertHTTPMethodFromProto(route.Method))
	return strings.ToUpper(key)
}

func ParseEnv(env string) *EnvironmentVariable {
	tokens := strings.Split(env, "=")
	return Env(tokens[0], tokens[1])
}
