package configurations

import (
	"context"
	"slices"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

type Loader interface {
	Identity() string
	Load(ctx context.Context, env *resources.Environment) error
	Configurations() []*basev0.Configuration
	DNS() []*basev0.DNS
}

type Manager struct {
	workspace *resources.Workspace
	services  map[string]*resources.Service

	loaders []Loader

	// Per Name in
	worspaceConfigurations map[string]*basev0.Configuration

	// Per service
	serviceConfigurations map[string]*basev0.Configuration

	exposedFromServiceConfigurations map[string][]*basev0.Configuration

	dns []*basev0.DNS

	reduced  []string
	doReduce bool
}

func NewManager(_ context.Context, workspace *resources.Workspace) (*Manager, error) {
	return &Manager{
		workspace:                        workspace,
		services:                         make(map[string]*resources.Service),
		worspaceConfigurations:           make(map[string]*basev0.Configuration),
		serviceConfigurations:            make(map[string]*basev0.Configuration),
		exposedFromServiceConfigurations: make(map[string][]*basev0.Configuration),
	}, nil
}

func (manager *Manager) WithLoader(loader Loader) *Manager {
	manager.loaders = append(manager.loaders, loader)
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

func (manager *Manager) GetWorkspaceConfigurations(_ context.Context) ([]*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	var out []*basev0.Configuration
	for _, conf := range manager.worspaceConfigurations {
		out = append(out, conf)
	}
	return out, nil
}

func (manager *Manager) GetWorkspaceDependenciesConfigurations(ctx context.Context, deps ...string) ([]*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	w := wool.Get(ctx).In("Manager.GetWorkspaceDependenciesConfigurations")
	var confs []*basev0.Configuration
	w.Focus("Found configurations", wool.Field("configurations", resources.MakeManyConfigurationSummary(confs)))

	var out []*basev0.Configuration
	for _, dep := range deps {
		if conf, ok := manager.worspaceConfigurations[dep]; ok {
			out = append(out, conf)
			continue
		}
		return nil, w.NewError("no configuration found for %s", dep)

	}
	return out, nil
}

func (manager *Manager) GetServiceConfigurations(_ context.Context) ([]*basev0.Configuration, error) {
	if manager == nil {
		return nil, nil
	}
	var out []*basev0.Configuration
	for _, conf := range manager.serviceConfigurations {
		out = append(out, conf)
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
	if manager == nil {
		return nil, nil
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
