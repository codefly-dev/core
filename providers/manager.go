package providers

import (
	"context"
	"slices"

	"github.com/hashicorp/go-multierror"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type Loader interface {
	Load(ctx context.Context, env *configurations.Environment) error
	Configurations() []*basev0.Configuration
	DNS() []*basev0.DNS
}

type Manager struct {
	project *configurations.Project
	loaders []Loader

	// Per Name in project
	projectConfigurations map[string]*basev0.Configuration

	// Per service
	serviceConfigurations map[string]*basev0.Configuration

	exposedFromServiceConfigurations map[string][]*basev0.Configuration

	dns []*basev0.DNS

	reduced  []string
	doReduce bool
}

func NewManager(_ context.Context, project *configurations.Project) (*Manager, error) {
	return &Manager{
		project:                          project,
		projectConfigurations:            make(map[string]*basev0.Configuration),
		serviceConfigurations:            make(map[string]*basev0.Configuration),
		exposedFromServiceConfigurations: make(map[string][]*basev0.Configuration),
	}, nil
}

func (manager *Manager) WithLoader(loader Loader) *Manager {
	manager.loaders = append(manager.loaders, loader)
	return manager
}

func (manager *Manager) Load(ctx context.Context, env *configurations.Environment) error {
	var agg error
	for _, loader := range manager.loaders {
		err := loader.Load(ctx, env)
		if err != nil {
			agg = multierror.Append(agg, err)
		}
	}
	err := manager.LoadConfigurations(ctx)
	if err != nil {
		agg = multierror.Append(agg, err)
	}
	err = manager.LoadDNS(ctx)
	if err != nil {
		agg = multierror.Append(agg, err)
	}
	return agg
}

func (manager *Manager) LoadConfigurations(_ context.Context) error {
	for _, loader := range manager.loaders {
		confs := loader.Configurations()
		for _, conf := range confs {
			if conf.Origin == configurations.ConfigurationProjectOrigin {
				for _, info := range conf.Configurations {
					if _, ok := manager.projectConfigurations[info.Name]; !ok {
						manager.projectConfigurations[info.Name] = &basev0.Configuration{
							Origin: configurations.ConfigurationProjectOrigin,
						}
					}
					manager.projectConfigurations[info.Name].Configurations = append(manager.projectConfigurations[info.Name].Configurations, info)
				}
			} else {
				if manager.skip(conf.Origin) {
					continue
				}
				for _, info := range conf.Configurations {
					if _, ok := manager.serviceConfigurations[conf.Origin]; !ok {
						manager.serviceConfigurations[conf.Origin] = &basev0.Configuration{
							Origin: conf.Origin,
						}
					}
					manager.serviceConfigurations[conf.Origin].Configurations = append(manager.serviceConfigurations[conf.Origin].Configurations, info)
				}
			}
		}
	}
	return nil
}

func (manager *Manager) GetProjectConfiguration(_ context.Context, name string) (*basev0.Configuration, error) {
	if conf, ok := manager.projectConfigurations[name]; ok {
		return conf, nil
	}
	return nil, nil
}

func (manager *Manager) GetServiceConfiguration(_ context.Context, service *configurations.Service) (*basev0.Configuration, error) {
	if conf, ok := manager.serviceConfigurations[service.Unique()]; ok {
		return conf, nil
	}
	return nil, nil
}

func (manager *Manager) GetConfigurations(ctx context.Context) ([]*basev0.Configuration, error) {
	w := wool.Get(ctx).In("providers.GetConfigurations")
	var out []*basev0.Configuration
	for _, conf := range manager.projectConfigurations {
		w.Debug("project configuration", wool.Field("got", conf))
		out = append(out, conf)
	}
	for svc, confs := range manager.serviceConfigurations {
		w.Debug("service configuration", wool.ServiceField(svc))
		out = append(out, confs)

	}
	for svc, confs := range manager.exposedFromServiceConfigurations {
		w.Debug("exposed service configuration", wool.ServiceField(svc))
		out = append(out, confs...)
	}
	return out, nil
}

func (manager *Manager) ExposeConfiguration(ctx context.Context, service *configurations.Service, confs ...*basev0.Configuration) error {
	w := wool.Get(ctx).In("Manager.ExposeConfiguration", wool.ThisField(service))
	w.Focus("exposing", wool.Field("configurations", configurations.MakeManyConfigurationSummary(confs)))
	if _, exists := manager.exposedFromServiceConfigurations[service.Unique()]; exists {
		return w.NewError("service already exposed")
	}
	manager.exposedFromServiceConfigurations[service.Unique()] = confs
	return nil
}

func (manager *Manager) GetSharedServiceConfiguration(_ context.Context, unique string) ([]*basev0.Configuration, error) {
	return manager.exposedFromServiceConfigurations[unique], nil
}

func (manager *Manager) Restrict(_ context.Context, values []*configurations.Service) error {
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
	return manager.dns
}

func (manager *Manager) GetDNS(_ context.Context, svc *configurations.Service, endpointName string) (*basev0.DNS, error) {
	for _, dns := range manager.dns {
		if svc.Project == dns.Project &&
			svc.Application == dns.Application &&
			dns.Service == svc.Name &&
			dns.Endpoint == endpointName {
			return dns, nil
		}
	}
	return nil, nil
}
