package providers

import (
	"context"
	"slices"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type ConfigurationInformationLoader interface {
	Load(ctx context.Context, env *configurations.Environment) ([]*basev0.Configuration, error)
}

type ConfigurationInformationManager struct {
	project *configurations.Project
	loaders []ConfigurationInformationLoader

	// Per Name in project
	projectConfigurations map[string]*basev0.Configuration

	// Per service
	serviceConfigurations map[string]*basev0.Configuration

	exposedFromServiceConfigurations map[string][]*basev0.Configuration

	reduced  []string
	doReduce bool
}

func NewConfigurationInformation(_ context.Context, project *configurations.Project) (*ConfigurationInformationManager, error) {
	return &ConfigurationInformationManager{
		project:                          project,
		projectConfigurations:            make(map[string]*basev0.Configuration),
		serviceConfigurations:            make(map[string]*basev0.Configuration),
		exposedFromServiceConfigurations: make(map[string][]*basev0.Configuration),
	}, nil
}

func (c *ConfigurationInformationManager) WithLoader(loader ConfigurationInformationLoader) *ConfigurationInformationManager {
	c.loaders = append(c.loaders, loader)
	return c
}

func (c *ConfigurationInformationManager) Load(ctx context.Context, env *configurations.Environment) error {
	w := wool.Get(ctx).In("providers.Load")
	for _, loader := range c.loaders {
		confs, err := loader.Load(ctx, env)
		if err != nil {
			return err
		}
		w.Debug("loaded configurations from loader", wool.Field("all", configurations.MakeManyConfigurationSummary(confs)))
		for _, conf := range confs {
			if conf.Origin == configurations.ConfigurationProjectOrigin {
				for _, info := range conf.Configurations {
					if _, ok := c.projectConfigurations[info.Name]; !ok {
						c.projectConfigurations[info.Name] = &basev0.Configuration{
							Origin: configurations.ConfigurationProjectOrigin,
						}
					}
					c.projectConfigurations[info.Name].Configurations = append(c.projectConfigurations[info.Name].Configurations, info)
				}
			} else {
				if c.skip(conf.Origin) {
					continue
				}
				for _, info := range conf.Configurations {
					if _, ok := c.serviceConfigurations[conf.Origin]; !ok {
						c.serviceConfigurations[conf.Origin] = &basev0.Configuration{
							Origin: conf.Origin,
						}
					}
					c.serviceConfigurations[conf.Origin].Configurations = append(c.serviceConfigurations[conf.Origin].Configurations, info)
				}
			}
		}
	}
	return nil
}

func (c *ConfigurationInformationManager) GetProjectConfiguration(_ context.Context, name string) (*basev0.Configuration, error) {
	if conf, ok := c.projectConfigurations[name]; ok {
		return conf, nil
	}
	return nil, nil
}

func (c *ConfigurationInformationManager) GetServiceConfiguration(_ context.Context, service *configurations.Service) (*basev0.Configuration, error) {
	if conf, ok := c.serviceConfigurations[service.Unique()]; ok {
		return conf, nil
	}
	return nil, nil
}

func (c *ConfigurationInformationManager) GetConfigurations(ctx context.Context) ([]*basev0.Configuration, error) {
	w := wool.Get(ctx).In("providers.GetConfigurations")
	var out []*basev0.Configuration
	for _, conf := range c.projectConfigurations {
		w.Debug("project configuration", wool.Field("got", conf))
		out = append(out, conf)
	}
	for svc, confs := range c.serviceConfigurations {
		w.Debug("service configuration", wool.ServiceField(svc))
		out = append(out, confs)

	}
	for svc, confs := range c.exposedFromServiceConfigurations {
		w.Debug("exposed service configuration", wool.ServiceField(svc))
		out = append(out, confs...)
	}
	return out, nil
}

func (c *ConfigurationInformationManager) Expose(ctx context.Context, service *configurations.Service, confs ...*basev0.Configuration) error {
	w := wool.Get(ctx).In("ConfigurationManager.Expose", wool.ThisField(service))
	w.Focus("exposing", wool.Field("configurations", configurations.MakeManyConfigurationSummary(confs)))
	if _, exists := c.exposedFromServiceConfigurations[service.Unique()]; exists {
		return w.NewError("service already exposed")
	}
	c.exposedFromServiceConfigurations[service.Unique()] = confs
	return nil
}

func (c *ConfigurationInformationManager) GetSharedServiceConfiguration(_ context.Context, unique string) ([]*basev0.Configuration, error) {
	return c.exposedFromServiceConfigurations[unique], nil
}

func (c *ConfigurationInformationManager) Restrict(_ context.Context, values []*configurations.Service) error {
	c.doReduce = true
	for _, svc := range values {
		c.reduced = append(c.reduced, svc.Unique())
	}
	return nil
}

func (c *ConfigurationInformationManager) skip(origin string) bool {
	return c.doReduce && !slices.Contains(c.reduced, origin)
}
