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

	// selfEndpoints are the service's own endpoints as its peers reach them.
	// They are emitted under SelfEndpointPrefix, never under EndpointPrefix,
	// so the listen address a service binds to is never overwritten by the
	// address it advertises.
	selfEndpoints []*EndpointAccess

	restRoutes []*RestRouteAccess
	running    bool
	fixture    string

	// templateDelivery says how a value carrying a ConfigurationValueTemplate
	// is delivered. It defaults to assembling the value, which is right for
	// every delivery that carries secrets; a render that must not carry them
	// sets it so the assembly comes from a secret reference instead.
	templateDelivery configurationTemplateDelivery

	// Per-service runtime overrides (KEY=VAL) injected via `codefly run ... --set`.
	overrides []*EnvironmentVariable

	// Other environment variables
	others []*EnvironmentVariable
}

func NewEnvironmentVariableManager() *EnvironmentVariableManager {
	return &EnvironmentVariableManager{}
}

// DeploymentScope returns a manager with the service-level environment and
// overrides but no inputs accumulated by earlier deployment requests.
func (holder *EnvironmentVariableManager) DeploymentScope() *EnvironmentVariableManager {
	return &EnvironmentVariableManager{
		environment:      holder.environment,
		workspace:        holder.workspace,
		module:           holder.module,
		service:          holder.service,
		version:          holder.version,
		runtimeContext:   holder.runtimeContext,
		fixture:          holder.fixture,
		templateDelivery: holder.templateDelivery,
		overrides:        cloneEnvironmentVariables(holder.overrides),
		others:           cloneEnvironmentVariables(holder.others),
	}
}

func cloneEnvironmentVariables(values []*EnvironmentVariable) []*EnvironmentVariable {
	result := make([]*EnvironmentVariable, len(values))
	for index, value := range values {
		if value == nil {
			continue
		}
		cloned := *value
		result[index] = &cloned
	}
	return result
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

func (holder *EnvironmentVariableManager) getBase() ([]*EnvironmentVariable, error) {
	var envs []*EnvironmentVariable

	if holder.running {
		envs = append(envs, Env(RunningPrefix, true))
	}

	fixture := holder.fixture
	if fixture == "" && holder.environment != nil {
		fixture = holder.environment.GetFixture()
	}
	if fixture != "" {
		envs = append(envs, FixtureAsEnvironmentVariable(fixture))
	}

	envs = append(envs, holder.overrides...)

	if holder.environment != nil {
		envs = append(envs, EnvironmentAsEnvironmentVariable(holder.environment))
		if holder.environment.NamingScope != "" {
			envs = append(envs, NamingScopeAsEnvironmentVariable(holder.environment))
		}
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

	for _, endpoint := range holder.selfEndpoints {
		envs = append(envs, SelfEndpointAsEnvironmentVariable(endpoint))
	}

	for _, restRoute := range holder.restRoutes {
		env, err := RestRoutesAsEnvironmentVariable(restRoute)
		if err != nil {
			return nil, err
		}
		envs = append(envs, env)
	}
	envs = append(envs, holder.others...)
	return envs, nil
}

func (holder *EnvironmentVariableManager) Endpoints() []*EndpointAccess {
	return holder.endpoints
}

// SelfEndpoints returns the service's own endpoints as recorded by
// AddSelfEndpoints: the addresses its peers use to reach it.
func (holder *EnvironmentVariableManager) SelfEndpoints() []*EndpointAccess {
	return holder.selfEndpoints
}

func (holder *EnvironmentVariableManager) All() ([]*EnvironmentVariable, error) {
	envs, err := holder.getBase()
	if err != nil {
		return nil, err
	}
	for _, conf := range holder.configurations {
		for _, secret := range []bool{false, true} {
			values, err := configurationAsEnvironmentVariables(conf, holder.environment.GetName(), secret, holder.templateDelivery)
			if err != nil {
				return nil, err
			}
			envs = append(envs, values...)
		}
	}
	for _, conf := range holder.rawConfigurations {
		values, err := ConfigurationAsRawEnvironmentVariables(conf)
		if err != nil {
			return nil, err
		}
		envs = append(envs, values...)
	}
	return envs, nil
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

// SetFixture records a fixture selection from a source that is not
// authoritative for the invocation. An empty selection is ignored and one
// already recorded is kept, so a caller with nothing to select — a StartRequest
// in a test flow that never carried a fixture — can neither clear nor override
// what the invocation selected.
func (holder *EnvironmentVariableManager) SetFixture(fixture string) {
	if fixture == "" || holder.fixture != "" {
		return
	}
	holder.fixture = fixture
}

// ResetFixture records the selection for a new invocation, replacing any
// previous one and clearing it when the invocation selects none. This manager
// outlives a single invocation whenever an agent process is reused for a second
// one, so the authoritative source has to be able to say "none" — otherwise the
// service keeps serving the fixture the previous invocation chose.
func (holder *EnvironmentVariableManager) ResetFixture(fixture string) {
	holder.fixture = fixture
}

// SetOverrides records overrides from a source that is not authoritative for
// the invocation. An empty set is ignored and one already recorded is kept, so
// a StartRequest in a test flow that never carried overrides can neither clear
// nor override what the invocation injected.
func (holder *EnvironmentVariableManager) SetOverrides(overrides map[string]string) {
	if len(overrides) == 0 || len(holder.overrides) > 0 {
		return
	}
	holder.AddOverrides(overrides)
}

// ResetOverrides records the overrides for a new invocation, replacing any
// previously recorded and clearing them when the invocation injects none. Like
// ResetFixture, this exists because the manager outlives a single invocation
// whenever an agent process is reused: otherwise the service keeps seeing the
// previous invocation's values.
func (holder *EnvironmentVariableManager) ResetOverrides(overrides map[string]string) {
	holder.overrides = nil
	holder.AddOverrides(overrides)
}

// AddOverrides appends per-service runtime overrides (KEY=VAL) so they reach
// the process environment via All()/Configurations(), like fixture/others.
func (holder *EnvironmentVariableManager) AddOverrides(overrides map[string]string) {
	for k, v := range overrides {
		holder.overrides = append(holder.overrides, Env(k, v))
	}
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

// DeliverConfigurationTemplatesByReference records that this render carries no
// secret values, so a value whose producer declared a template is delivered by
// the secret reference declared for its carrier rather than assembled here. Only
// a render that supplies those references may set it: without one the credential
// is absent from the workload, which the restricted deployment gate is what
// checks.
func (holder *EnvironmentVariableManager) DeliverConfigurationTemplatesByReference() {
	holder.templateDelivery = deliverConfigurationTemplateByReference
}

const WorkspaceConfigurationPrefix = "CODEFLY__WORKSPACE_CONFIGURATION"

// #nosec G101
const WorkspaceSecretConfigurationPrefix = "CODEFLY__WORKSPACE_SECRET_CONFIGURATION"
const ServiceConfigurationPrefix = "CODEFLY__SERVICE_CONFIGURATION"
const ServiceSecretConfigurationPrefix = "CODEFLY__SERVICE_SECRET_CONFIGURATION"

func (holder *EnvironmentVariableManager) Configurations() ([]*EnvironmentVariable, error) {
	envs, err := holder.getBase()
	if err != nil {
		return nil, err
	}
	for _, conf := range holder.configurations {
		values, err := configurationAsEnvironmentVariables(conf, holder.environment.GetName(), false, holder.templateDelivery)
		if err != nil {
			return nil, err
		}
		envs = append(envs, values...)
	}
	return envs, nil
}

func (holder *EnvironmentVariableManager) Secrets() ([]*EnvironmentVariable, error) {
	var envs []*EnvironmentVariable
	for _, conf := range holder.configurations {
		values, err := configurationAsEnvironmentVariables(conf, holder.environment.GetName(), true, holder.templateDelivery)
		if err != nil {
			return nil, err
		}
		envs = append(envs, values...)
	}
	return envs, nil
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
		for _, info := range conf.GetInfos() {
			if info.GetData() != nil {
				return fmt.Errorf("structured configuration requires the scoped configuration carrier")
			}
		}
	}
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
	// Nil-safe: a mapping without an endpoint is treated as non-public, never a panic.
	if nm != nil && nm.Endpoint != nil && nm.Endpoint.Visibility == VisibilityPublic {
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
		if mp == nil {
			continue
		}
		for _, instance := range mp.Instances {
			if accessKindMatches(instance, networkAccess) {
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

// AddSelfEndpoints records the service's OWN endpoints as its peers reach them,
// selecting from each mapping the instance whose access matches networkAccess.
//
// This is distinct from AddEndpoints: a service's CODEFLY__ENDPOINT__ carrier
// for its own endpoint is the address it LISTENS on (a deployment localizes it
// to localhost), which is the wrong answer when the service must tell another
// component how to call it back — registering an upstream with a gateway, say.
// The advertised address is carried separately under SelfEndpointPrefix so the
// two can never be confused. Callers pass the access their peers use: container
// access for a Kubernetes render (the in-cluster Service DNS name), and the
// runtime's own access for a local run.
func (holder *EnvironmentVariableManager) AddSelfEndpoints(ctx context.Context, mappings []*basev0.NetworkMapping, networkAccess *basev0.NetworkAccess) error {
	w := wool.Get(ctx).In("configurations.EnvironmentVariableManager.AddSelfEndpoints")
	for _, mp := range mappings {
		if mp == nil || mp.Endpoint == nil {
			continue
		}
		for _, instance := range mp.Instances {
			if accessKindMatches(instance, networkAccess) {
				holder.selfEndpoints = append(holder.selfEndpoints, &EndpointAccess{
					Endpoint:        mp.Endpoint,
					NetworkInstance: instance,
				})
				break
			}
		}
	}
	w.Debug("added self endpoints", wool.SliceCountField(holder.selfEndpoints))
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

// FindSelfNetworkInstanceInEnvironmentVariables is the SDK accessor for a
// service's advertised address: the value of its
// CODEFLY__SELF_ENDPOINT__<MODULE>__<SERVICE>__<NAME>__<API> carrier, which is
// how its peers reach the endpoint. Use FindNetworkInstanceInEnvironmentVariables
// for the address to listen on. A missing carrier is an error, never a fallback
// to the listen address: advertising localhost to a peer is the defect this
// carrier exists to prevent.
func FindSelfNetworkInstanceInEnvironmentVariables(ctx context.Context, endpointInfo *EndpointInformation, envs []string) (*NetworkInstance, error) {
	w := wool.Get(ctx).In("configurations.EnvironmentVariableManager.FindSelfNetworkInstance")
	key := SelfEndpointAsEnvironmentVariableKey(endpointInfo)
	w.Trace("searching for self network instance", wool.NameField(key))
	for _, env := range envs {
		if after, found := strings.CutPrefix(env, fmt.Sprintf("%s=", key)); found {
			return ParseAddress(after)
		}
	}
	return nil, w.NewError("no self endpoint %s found", key)
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
	prefix   string
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
							prefix:   prefix(mp, opt),
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

const NamingScopePrefix = "CODEFLY__NAMING_SCOPE"

func NamingScopeAsEnvironmentVariable(env *basev0.Environment) *EnvironmentVariable {
	return Env(NamingScopePrefix, env.NamingScope)
}

func IsLocal(environment *basev0.Environment) bool {
	return environment.Name == "local"
}

const EndpointPrefix = "CODEFLY__ENDPOINT"

func EndpointAsEnvironmentVariableKeyBase(info *EndpointInformation) string {
	key := fmt.Sprintf("%s__%s__%s__%s", info.Module, info.Service, info.Name, info.API)
	key = strings.ReplaceAll(key, "-", "_")
	return strings.ToUpper(key)
}

func EndpointAsEnvironmentVariableKey(info *EndpointInformation) string {
	return strings.ToUpper(fmt.Sprintf("%s__%s", EndpointPrefix, EndpointAsEnvironmentVariableKeyBase(info)))
}

// SelfEndpointPrefix carries a service's own endpoints as its peers reach
// them. It deliberately does not start with EndpointPrefix + "__", so a reader
// collecting CODEFLY__ENDPOINT__ carriers never mistakes one for the other.
const SelfEndpointPrefix = "CODEFLY__SELF_ENDPOINT"

// SelfEndpointAsEnvironmentVariableKey names the advertised-address carrier
// with the same module/service/name/api normalization as the endpoint carrier.
func SelfEndpointAsEnvironmentVariableKey(info *EndpointInformation) string {
	return strings.ToUpper(fmt.Sprintf("%s__%s", SelfEndpointPrefix, EndpointAsEnvironmentVariableKeyBase(info)))
}

// SelfEndpointAsEnvironmentVariable renders one advertised endpoint. The
// value has the same shape as an endpoint carrier's (NetworkInstance.Address:
// host:port, or a scheme-qualified URL for HTTP-based APIs).
func SelfEndpointAsEnvironmentVariable(endpointAccess *EndpointAccess) *EnvironmentVariable {
	info := EndpointInformationFromProto(endpointAccess.Endpoint)
	return Env(SelfEndpointAsEnvironmentVariableKey(info), endpointAccess.NetworkInstance.Address)
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

// configurationTemplateDelivery says how a value whose producer declared a
// ConfigurationValueTemplate instead of a value reaches the workload.
type configurationTemplateDelivery int

const (
	// deliverConfigurationTemplateByValue assembles the template where the
	// primitives are and emits the assembled value. This is every delivery that
	// already carries secret values: a native or container run, a local apply.
	deliverConfigurationTemplateByValue configurationTemplateDelivery = iota
	// deliverConfigurationTemplateByReference omits the value: the render must
	// carry no secret values, so the assembly is delivered by the secret
	// reference declared for its carrier. Emitting it here would either write
	// the secret into the render or, as the empty string the value literally
	// holds, silently unset the credential.
	deliverConfigurationTemplateByReference
)

// ConfigurationValueEnvironmentKey returns the environment carrier name one
// configuration value is delivered under — the secret carrier for a secret
// value, the public one otherwise. A caller that must name a carrier without
// emitting it, like the restricted deployment gate looking for the secret
// reference that will deliver a templated value, goes through this rather than
// rebuilding the name and drifting from it.
func ConfigurationValueEnvironmentKey(conf *basev0.Configuration, informationName string, value *basev0.ConfigurationValue) string {
	key := fmt.Sprintf("%s__%s__%s", ConfigurationEnvironmentKeyPrefix(conf), NameToKey(informationName), NameToKey(value.GetKey()))
	if !value.GetSecret() {
		return key
	}
	key = strings.Replace(key, WorkspaceConfigurationPrefix, WorkspaceSecretConfigurationPrefix, 1)
	return strings.Replace(key, ServiceConfigurationPrefix, ServiceSecretConfigurationPrefix, 1)
}

// ConfigurationAsEnvironmentVariables converts a configuration to a list of environment variables
// the secret flag decides if we return secret or regular values
func ConfigurationAsEnvironmentVariables(conf *basev0.Configuration, environment string, secret bool) ([]*EnvironmentVariable, error) {
	return configurationAsEnvironmentVariables(conf, environment, secret, deliverConfigurationTemplateByValue)
}

func configurationAsEnvironmentVariables(conf *basev0.Configuration, environment string, secret bool, delivery configurationTemplateDelivery) ([]*EnvironmentVariable, error) {
	var env []*EnvironmentVariable
	if conf == nil {
		return env, nil
	}
	for _, info := range conf.Infos {
		if info == nil {
			return nil, fmt.Errorf("configuration information must not be nil")
		}
		if data := info.Data; data != nil && data.Secret == secret {
			variable, err := configurationDocumentVariable(conf.Origin, info.Name, environment, data)
			if err != nil {
				return nil, err
			}
			env = append(env, variable)
		}
		for _, value := range info.ConfigurationValues {
			if value == nil {
				return nil, fmt.Errorf("configuration value must not be nil")
			}
			if err := ValidateTemplatedConfigurationValue(value); err != nil {
				return nil, fmt.Errorf("configuration %q from %q: %w", info.Name, conf.Origin, err)
			}
			// if secret: only add secret values
			if value.Secret != secret {
				continue
			}
			if value.GetTemplate() != nil && delivery == deliverConfigurationTemplateByReference {
				continue
			}
			resolved, err := ConfigurationValueAsString(conf, value)
			if err != nil {
				return nil, fmt.Errorf("configuration %q key %q from %q: %w", info.Name, value.Key, conf.Origin, err)
			}
			env = append(env, Env(ConfigurationValueEnvironmentKey(conf, info.Name, value), resolved))
		}
	}
	return env, nil
}

// ConfigurationAsRawEnvironmentVariables preserves explicitly named flat keys.
// Structured documents require a scoped carrier and cannot use this path.
func ConfigurationAsRawEnvironmentVariables(conf *basev0.Configuration) ([]*EnvironmentVariable, error) {
	var env []*EnvironmentVariable
	for _, info := range conf.GetInfos() {
		if info == nil {
			return nil, fmt.Errorf("configuration information must not be nil")
		}
		if info.Data != nil {
			return nil, fmt.Errorf("structured configuration requires the scoped configuration carrier")
		}
		for _, value := range info.ConfigurationValues {
			if value == nil {
				return nil, fmt.Errorf("configuration value must not be nil")
			}
			if err := ValidateTemplatedConfigurationValue(value); err != nil {
				return nil, fmt.Errorf("configuration %q: %w", info.GetName(), err)
			}
			resolved, err := ConfigurationValueAsString(conf, value)
			if err != nil {
				return nil, fmt.Errorf("configuration %q key %q: %w", info.GetName(), value.Key, err)
			}
			env = append(env, Env(value.Key, resolved))
		}
	}
	return env, nil
}

func ServiceConfigurationKeyFromUnique(unique string, name string, key string) string {
	return fmt.Sprintf("%s__%s__%s", ServiceConfigurationEnvironmentKeyPrefixFromUnique(unique), NameToKey(name), NameToKey(key))
}

func ServiceConfigurationKey(service *ServiceIdentity, name string, key string) string {
	return ServiceConfigurationKeyFromUnique(service.Unique(), name, key)
}

func ServiceSecretConfigurationKeyFromUnique(unique string, name string, key string) string {
	return fmt.Sprintf("%s__%s__%s", ServiceSecretConfigurationEnvironmentKeyPrefixFromUnique(unique), NameToKey(name), NameToKey(key))
}

func ServiceSecretConfigurationKey(service *ServiceIdentity, name string, key string) string {
	return ServiceSecretConfigurationKeyFromUnique(service.Unique(), name, key)
}

func NameToKey(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
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

func RestRoutesAsEnvironmentVariable(restRoute *RestRouteAccess) (*EnvironmentVariable, error) {
	key, err := RestRouteEnvironmentVariableKey(EndpointInformationFromProto(restRoute.endpoint), restRoute.route)
	if err != nil {
		return nil, err
	}
	if restRoute.prefix != "" {
		key = fmt.Sprintf("%s%s", restRoute.prefix, key)
	}
	return Env(key, restRoute.endpoint.Visibility), nil
}

func RestRouteEnvironmentVariableKey(info *EndpointInformation, route *basev0.RestRoute) (string, error) {
	key := EndpointAsEnvironmentVariableKeyBase(info)
	// Add path
	key = fmt.Sprintf("%s__%s", RestRoutePrefix, key)
	key = fmt.Sprintf("%s___%s", key, sanitizePath(route.Path))
	method, err := ConvertHTTPMethodFromProto(route.Method)
	if err != nil {
		return "", err
	}
	key = fmt.Sprintf("%s___%s", key, method)
	return strings.ToUpper(key), nil
}

func ParseEnv(env string) *EnvironmentVariable {
	tokens := strings.SplitN(env, "=", 2)
	if len(tokens) < 2 {
		return Env(env, "")
	}
	return Env(tokens[0], tokens[1])
}
