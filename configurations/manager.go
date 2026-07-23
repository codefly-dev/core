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

type Manager struct {
	workspace *resources.Workspace
	services  map[string]*resources.Service

	loaders []Loader

	// Secret resolvers registered explicitly (tests, custom backends). The
	// environment's own `secrets.provider` adds to these at Load() time.
	secretResolvers []SecretResolver

	// Per Name in
	worspaceConfigurations map[string]*basev0.Configuration

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
}

func NewManager(_ context.Context, workspace *resources.Workspace) (*Manager, error) {
	return &Manager{
		workspace:                        workspace,
		services:                         make(map[string]*resources.Service),
		worspaceConfigurations:           make(map[string]*basev0.Configuration),
		serviceConfigurations:            make(map[string]*basev0.Configuration),
		exposedFromServiceConfigurations: make(map[string][]*basev0.Configuration),
		resolvedWorkspace:                make(map[string]bool),
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
		out = append(out, conf)
	}
	return out, nil
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
		out = append(out, conf)
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
