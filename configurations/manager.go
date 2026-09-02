package configurations

import (
	"context"
	"fmt"
	"slices"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

type Loader interface {
	Identity() string
	Load(ctx context.Context, env *resources.Environment) error
	// Configurations returns the configurations produced by Load. It must
	// return the same instances on every call (not fresh copies): after Load,
	// the Manager resolves secret references in these objects in place, and
	// LoadConfigurations reads them back expecting the resolved values.
	Configurations() []*basev0.Configuration
	DNS() []*basev0.DNS
}

// compositionRootConfigurationsLoader is an optional capability a Loader may
// implement to tell the Manager which of the workspace configurations it
// produced originate from the composition root itself, rather than from a
// composed module. The Manager injects the former into every service in the run
// (see GetCompositionRootWorkspaceConfigurations).
type compositionRootConfigurationsLoader interface {
	CompositionRootWorkspaceConfigurationNames() []string
}

type Manager struct {
	workspace *resources.Workspace
	services  map[string]*resources.Service

	loaders []Loader

	// Secret resolvers registered explicitly (tests, custom backends). The
	// environment's own `secrets.provider` adds to these at Load() time.
	secretResolvers []SecretResolver

	// Per Name in
	worspaceConfigurations map[string]*basev0.Configuration

	// Names of the workspace configurations the composition root itself provides,
	// injected into every service in the run (as opposed to those a composed
	// module carries only for the services that declare them).
	compositionRootWorkspaceConfigurations map[string]bool

	// Per service
	serviceConfigurations map[string]*basev0.Configuration

	exposedFromServiceConfigurations map[string][]*basev0.Configuration

	dns []*basev0.DNS

	reduced  []string
	doReduce bool

	// resolution and env are captured at Load() so workspace-origin secrets,
	// deferred until a caller selects them, resolve through the same per-load
	// URI cache the service-origin pass already used.
	resolution        *secretResolution
	env               *resources.Environment
	resolvedWorkspace map[string]bool

	// Run network mappings used to resolve ${endpoint:…} references in workspace
	// configuration values, alongside the same secret resolution.
	networkMappings []*basev0.NetworkMapping
	networkAccess   *basev0.NetworkAccess
}

func NewManager(_ context.Context, workspace *resources.Workspace) (*Manager, error) {
	return &Manager{
		workspace:                              workspace,
		services:                               make(map[string]*resources.Service),
		worspaceConfigurations:                 make(map[string]*basev0.Configuration),
		compositionRootWorkspaceConfigurations: make(map[string]bool),
		serviceConfigurations:                  make(map[string]*basev0.Configuration),
		exposedFromServiceConfigurations:       make(map[string][]*basev0.Configuration),
		resolvedWorkspace:                      make(map[string]bool),
	}, nil
}

func (manager *Manager) WithLoader(loader Loader) *Manager {
	manager.loaders = append(manager.loaders, loader)
	return manager
}

// WithSecretResolver registers a secret resolver. Resolvers selected by the
// environment's `secrets` block are added automatically at Load() time; this
// is for tests and custom backends.
func (manager *Manager) WithSecretResolver(resolvers ...SecretResolver) *Manager {
	manager.secretResolvers = append(manager.secretResolvers, resolvers...)
	return manager
}

// WithNetworkMappings supplies the run's network mappings and the network access
// against which ${endpoint:…} references in workspace configuration values are
// resolved. The composition root sets these once ports are allocated, before any
// workspace configuration is selected.
func (manager *Manager) WithNetworkMappings(mappings []*basev0.NetworkMapping, access *basev0.NetworkAccess) *Manager {
	manager.networkMappings = mappings
	manager.networkAccess = access
	return manager
}

func (manager *Manager) Load(ctx context.Context, env *resources.Environment) error {
	if manager == nil {
		return nil
	}
	w := wool.Get(ctx).In("providers.Load")

	for _, loader := range manager.loaders {
		err := loader.Load(ctx, env)
		if err != nil {
			return w.Wrapf(err, "cannot load loader %s", loader.Identity())
		}
	}
	if err := manager.resolveSecrets(ctx, env); err != nil {
		return w.Wrapf(err, "cannot resolve secrets")
	}
	err := manager.LoadConfigurations(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot load configurations")
	}

	err = manager.LoadDNS(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot load DNS")
	}

	w.Debug("loaded", wool.Field("dns", resources.MakeManyDNSSummary(manager.dns)))
	return nil
}

// resolveSecrets resolves reference-valued secrets (op://…) produced by the
// loaders, in place, before they are consolidated. Plaintext secret values
// pass through untouched. Workspace-origin configurations are deferred: Core
// does not yet know which dependencies a caller selects, so their references
// are resolved lazily by resolveWorkspaceConfiguration once names arrive.
func (manager *Manager) resolveSecrets(ctx context.Context, env *resources.Environment) error {
	w := wool.Get(ctx).In("configurations.Manager.resolveSecrets")
	fromEnv, err := ResolversFromEnvironment(env)
	if err != nil {
		return w.Wrapf(err, "cannot build secret resolvers for environment %s", env.Name)
	}
	resolvers := append(append([]SecretResolver{}, manager.secretResolvers...), fromEnv...)
	manager.resolution = newSecretResolution(resolvers)
	manager.env = env
	for _, loader := range manager.loaders {
		for _, conf := range loader.Configurations() {
			if conf.Origin == resources.ConfigurationWorkspace {
				continue
			}
			if manager.skip(conf.Origin) {
				continue
			}
			if err := manager.resolution.resolveConfiguration(ctx, conf, env); err != nil {
				return w.Wrapf(err, "cannot resolve secrets from loader %s", loader.Identity())
			}
		}
	}
	return nil
}

// resolveWorkspaceConfiguration resolves a selected workspace configuration in
// place, at most once per load. The per-load URI cache is shared with every
// other selected configuration and with the service-origin pass, so a reference
// used by several is fetched from its provider only once.
func (manager *Manager) resolveWorkspaceConfiguration(ctx context.Context, name string, conf *basev0.Configuration) error {
	if manager.resolvedWorkspace[name] {
		return nil
	}
	if manager.resolution != nil {
		if err := manager.resolution.resolveConfiguration(ctx, conf, manager.env); err != nil {
			return err
		}
	}
	manager.resolvedWorkspace[name] = true
	return nil
}

// LoadConfigurations fetch different loaders and consolidate
func (manager *Manager) LoadConfigurations(_ context.Context) error {
	for _, loader := range manager.loaders {
		if provider, ok := loader.(compositionRootConfigurationsLoader); ok {
			for _, name := range provider.CompositionRootWorkspaceConfigurationNames() {
				manager.compositionRootWorkspaceConfigurations[name] = true
			}
		}
		confs := loader.Configurations()
		for _, conf := range confs {
			if conf.Origin == resources.ConfigurationWorkspace {
				for _, info := range conf.Infos {
					if _, ok := manager.worspaceConfigurations[info.Name]; !ok {
						manager.worspaceConfigurations[info.Name] = &basev0.Configuration{
							Origin: resources.ConfigurationWorkspace,
						}
					}
					manager.worspaceConfigurations[info.Name].Infos = append(manager.worspaceConfigurations[info.Name].Infos, info)
				}
				continue
			}
			if manager.skip(conf.Origin) {
				continue
			}
			for _, info := range conf.Infos {
				if _, ok := manager.serviceConfigurations[conf.Origin]; !ok {
					manager.serviceConfigurations[conf.Origin] = &basev0.Configuration{
						Origin: conf.Origin,
					}
				}
				manager.serviceConfigurations[conf.Origin].Infos = append(manager.serviceConfigurations[conf.Origin].Infos, info)
			}
		}
	}
	return nil
}

func (manager *Manager) GetWorkspaceConfigurations(ctx context.Context) ([]*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	w := wool.Get(ctx).In("Manager.GetWorkspaceConfigurations")
	names := make([]string, 0, len(manager.worspaceConfigurations))
	for name := range manager.worspaceConfigurations {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]*basev0.Configuration, 0, len(names))
	for _, name := range names {
		conf := manager.worspaceConfigurations[name]
		if err := manager.resolveWorkspaceConfiguration(ctx, name, conf); err != nil {
			return nil, w.Wrapf(err, "cannot resolve workspace configuration %s", name)
		}
		resolved, err := manager.interpolateEndpoints(ctx, name, conf)
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

// GetCompositionRootWorkspaceConfigurations returns the workspace configurations
// the composition root itself provides to the whole run — its own
// configurations/<profile>/* and any invocation-scoped override — as opposed to
// the configurations a composed module carries only for the services that
// declare them as dependencies. The composition root injects these into every
// service in the run, so a composed-module service reading
// WorkspaceValue(name, key) resolves a root-provided value it never had to
// redeclare.
//
// This set and the per-dependency composed-module set are disjoint by name: a
// name the composition root also declares is resolved to the root at load
// (composeModuleWorkspaceConfigurations suppresses the module's), so the root
// fills what a composed module leaves unset and never shadows a name only the
// module provides.
//
// Values are secret-resolved and endpoint-interpolated for the access set on the
// Manager, exactly like GetWorkspaceConfigurations — and, like it, a bad
// ${endpoint:…} reference is a hard error, since every returned configuration is
// injected run-wide.
func (manager *Manager) GetCompositionRootWorkspaceConfigurations(ctx context.Context) ([]*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	w := wool.Get(ctx).In("Manager.GetCompositionRootWorkspaceConfigurations")
	names := make([]string, 0, len(manager.compositionRootWorkspaceConfigurations))
	for name := range manager.compositionRootWorkspaceConfigurations {
		if _, ok := manager.worspaceConfigurations[name]; ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	out := make([]*basev0.Configuration, 0, len(names))
	for _, name := range names {
		conf := manager.worspaceConfigurations[name]
		if err := manager.resolveWorkspaceConfiguration(ctx, name, conf); err != nil {
			return nil, w.Wrapf(err, "cannot resolve workspace configuration %s", name)
		}
		resolved, err := manager.interpolateEndpoints(ctx, name, conf)
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

// interpolateEndpoints resolves ${endpoint:…} references against the run's network
// mappings for the configured access. The address is consumer-specific, so it is
// resolved on the way out (never cached into the shared configuration): the
// composition root sets the consumer's access via WithNetworkMappings before each
// read.
func (manager *Manager) interpolateEndpoints(ctx context.Context, name string, conf *basev0.Configuration) (*basev0.Configuration, error) {
	w := wool.Get(ctx).In("Manager.interpolateEndpoints")
	resolved, err := resources.InterpolateConfigurationEndpoints(ctx, conf, manager.networkMappings, manager.networkAccess)
	if err != nil {
		return nil, w.Wrapf(err, "cannot interpolate workspace configuration %s", name)
	}
	return resolved, nil
}

func (manager *Manager) GetWorkspaceDependenciesConfigurations(ctx context.Context, deps ...string) ([]*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	w := wool.Get(ctx).In("Manager.GetWorkspaceDependenciesConfigurations")
	out := make([]*basev0.Configuration, 0, len(deps))
	for _, dep := range deps {
		conf, ok := manager.worspaceConfigurations[dep]
		if !ok {
			return nil, w.NewError("no configuration found for %s", dep)
		}
		if err := manager.resolveWorkspaceConfiguration(ctx, dep, conf); err != nil {
			return nil, w.Wrapf(err, "cannot resolve workspace configuration %s", dep)
		}
		resolved, err := manager.interpolateEndpoints(ctx, dep, conf)
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

func (manager *Manager) GetServiceConfigurations(_ context.Context) ([]*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	origins := make([]string, 0, len(manager.serviceConfigurations))
	for origin := range manager.serviceConfigurations {
		origins = append(origins, origin)
	}
	slices.Sort(origins)
	out := make([]*basev0.Configuration, 0, len(origins))
	for _, origin := range origins {
		out = append(out, manager.serviceConfigurations[origin])
	}
	return out, nil
}

func (manager *Manager) GetServiceConfiguration(_ context.Context, service *resources.ServiceIdentity) (*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	if conf, ok := manager.serviceConfigurations[service.Unique()]; ok {
		return conf, nil
	}
	return nil, nil
}

func (manager *Manager) ExposeConfiguration(ctx context.Context, service *resources.ServiceIdentity, confs ...*basev0.Configuration) error {
	if manager == nil {
		return nil
	}
	w := wool.Get(ctx).In("Manager.ExposeConfiguration", wool.ThisField(service))
	w.Debug("exposing", wool.Field("configurations", resources.MakeManyConfigurationSummary(confs)))
	manager.exposedFromServiceConfigurations[service.Unique()] = confs
	return nil
}

func (manager *Manager) GetSharedServiceConfiguration(_ context.Context, unique string) ([]*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	return manager.exposedFromServiceConfigurations[unique], nil
}

func (manager *Manager) Restrict(_ context.Context, values []*resources.ServiceIdentity) error {
	if manager == nil {
		return nil
	}
	manager.doReduce = true
	for _, svc := range values {
		manager.reduced = append(manager.reduced, svc.Unique())
	}
	return nil
}

func (manager *Manager) skip(origin string) bool {
	return manager.doReduce && !slices.Contains(manager.reduced, origin)
}

func (manager *Manager) LoadDNS(_ context.Context) error {
	for _, loader := range manager.loaders {
		manager.dns = append(manager.dns, loader.DNS()...)
	}
	return nil
}

func (manager *Manager) DNS() []*basev0.DNS {
	if manager == nil {
		return nil
	}
	return manager.dns
}

func (manager *Manager) GetDNS(ctx context.Context, svc *resources.ServiceIdentity, endpointName string) (*basev0.DNS, error) {
	// Returning (nil, error) on a nil receiver lets callers distinguish
	// "uninitialized manager" from "manager has no matching DNS entry".
	// The previous (nil, nil) return swallowed the misconfiguration —
	// network/remote_manager.go would then nil-deref on the result.
	if manager == nil {
		return nil, fmt.Errorf("configurations.Manager: receiver is nil — DNS lookup attempted before Manager initialization")
	}
	w := wool.Get(ctx).In("providers.GetDNS", wool.ThisField(svc))
	for _, dns := range manager.dns {
		if svc.Module == dns.Module &&
			dns.Service == svc.Name &&
			dns.Endpoint == endpointName {
			return dns, nil
		}
	}
	return nil, w.NewError("no DNS found: %s::%s. Available: %s", svc.Unique(), endpointName, resources.MakeManyDNSSummary(manager.dns))
}
