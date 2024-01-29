package services

import (
	"context"
	"embed"
	"fmt"
	"path"

	"github.com/codefly-dev/core/agents/network"
	"github.com/codefly-dev/core/builders"

	"github.com/codefly-dev/core/configurations/standards"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/agents/communicate"
	"github.com/codefly-dev/core/agents/helpers/code"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
)

type Information struct {
	Service *configurations.ServiceWithCase
	Agent   *configurations.Agent
	Domain  string
}

type Base struct {
	// Agent
	Agent     *configurations.Agent
	WoolAgent *wool.Provider
	Wool      *wool.Wool

	// Continuity check
	loaded bool

	// Underlying service
	WoolService *wool.Provider

	// State
	Identity *configurations.ServiceIdentity
	Location string

	EnvironmentVariables *configurations.EnvironmentVariableManager

	// codefly configuration
	ConfigurationLocation string

	Configuration *configurations.Service

	// Information convenience
	Information *Information

	// Endpoints
	Endpoints           []*basev0.Endpoint
	DependencyEndpoints []*basev0.Endpoint

	NetworkMappings []*basev0.NetworkMapping

	// Wrappers
	Runtime *RuntimeWrapper
	Builder *BuilderWrapper

	// Runtime
	State InformationStatus

	// Communication
	Communication *communicate.Server

	// Code Watcher
	Watcher *code.Watcher
	Events  chan code.Change
}

func NewServiceBase(ctx context.Context, agent *configurations.Agent) *Base {
	provider := agents.NewAgentProvider(ctx, agent)
	base := &Base{
		Agent:                agent,
		EnvironmentVariables: configurations.NewEnvironmentVariableManager(),
		Communication:        communicate.NewServer(ctx),
		WoolAgent:            provider,
		Wool:                 provider.Get(ctx),
	}
	base.Runtime = &RuntimeWrapper{Base: base}
	base.Builder = &BuilderWrapper{Base: base}
	return base
}

func (s *Base) Unique() string {
	return s.Configuration.Unique()
}

func (s *Base) Load(ctx context.Context, identity *basev0.ServiceIdentity, settings any) error {
	s.Identity = configurations.ServiceIdentityFromProto(identity)
	s.Location = identity.Location

	// Replace the agent now that we know more!
	s.WoolAgent = agents.NewServiceProvider(ctx, s.Identity)

	s.Wool = s.WoolAgent.Get(ctx)

	ctx = s.Wool.Inject(ctx)

	s.ConfigurationLocation = path.Join(s.Location, "codefly")
	_, err := shared.CheckDirectoryOrCreate(ctx, s.ConfigurationLocation)

	if err != nil {
		return s.Wool.Wrapf(err, "cannot create configuration directory")
	}

	s.Configuration, err = configurations.LoadServiceFromDirUnsafe(ctx, s.Location)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot load service configuration")
	}

	err = s.Configuration.LoadSettingsFromSpec(settings)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot load settings from spec")
	}

	s.Information = &Information{
		Service: configurations.ToServiceWithCase(s.Configuration),
		Domain:  s.Identity.Domain,
		Agent:   s.Agent,
	}
	s.loaded = true
	return nil
}

func (s *Base) DockerImage() *configurations.DockerImage {
	return &configurations.DockerImage{
		Name: fmt.Sprintf("%s/%s", s.Identity.Application, s.Identity.Name),
		Tag:  s.Version().Version,
	}
}

// EndpointsFromConfiguration from Configuration and data from the service
func (s *Base) EndpointsFromConfiguration(ctx context.Context) ([]*basev0.Endpoint, error) {
	var eps []*basev0.Endpoint
	for _, e := range s.Configuration.Endpoints {
		if e.API == standards.GRPC {
			endpoint, err := configurations.NewGrpcAPI(ctx, e, s.Local("api.proto"))
			if err != nil {
				return nil, s.Wool.Wrapf(err, "cannot create grpc api")
			}
			eps = append(eps, endpoint)
			continue
		}
		if e.API == standards.REST {
			endpoint, err := configurations.NewRestAPIFromOpenAPI(ctx, e, s.Local("api.swagger.json"))
			if err != nil {
				return nil, s.Wool.Wrapf(err, "cannot create grpc api")
			}
			eps = append(eps, endpoint)
			continue
		}
	}
	return eps, nil
}

func (s *Base) SetupNetworkMappings(ctx context.Context) error {
	pm, err := network.NewServicePortManager(ctx)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot create default endpoint")
	}
	for _, endpoint := range s.Endpoints {
		s.Wool.Focus("exposing", wool.Field("destination", configurations.EndpointDestination(endpoint)))
		err = pm.Expose(endpoint)
		if err != nil {
			return s.Wool.Wrapf(err, "cannot add grpc endpoint to network manager")
		}
	}
	err = pm.Reserve(ctx)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot reserve ports")
	}
	s.NetworkMappings, err = pm.NetworkMapping(ctx)
	s.Wool.Focus("network mappings", wool.Field("mappings", configurations.MakeNetworkMappingSummary(s.NetworkMappings)))
	if err != nil {
		return s.Wool.Wrapf(err, "cannot create network mapping")
	}
	return nil
}

type WatchConfiguration struct {
	dependencies *builders.Dependencies
}

func NewWatchConfiguration(dependencies *builders.Dependencies) *WatchConfiguration {
	return &WatchConfiguration{
		dependencies: dependencies,
	}
}

func (s *Base) SetupWatcher(ctx context.Context, conf *WatchConfiguration, handler func(event code.Change) error) error {
	s.Wool.Debug("watching for changes", wool.Field("dependencies", builders.MakeDependenciesSummary(conf.dependencies)))
	s.Events = make(chan code.Change)
	var err error
	s.Watcher, err = code.NewWatcher(ctx, s.Events, s.Location, conf.dependencies)
	if err != nil {
		return err
	}
	go s.Watcher.Start(ctx)

	go func() {
		for event := range s.Events {
			err := handler(event)
			if err != nil {
				s.Wool.Error("got", wool.ErrField(err))
			}
		}
	}()
	return nil
}

func (s *Base) Local(f string) string {
	return path.Join(s.Location, f)
}

/* Some very important helpers */

func (s *Base) Errorf(format string, args ...any) error {
	return s.Wool.NewError(format, args...)
}

func (s *Base) Info(msg string, fields ...*wool.LogField) {
	s.Wool.Info(msg, fields...)
}

func (s *Base) Debug(msg string, fields ...*wool.LogField) {
	s.Wool.Debug(msg, fields...)
}

func (s *Base) Focus(msg string, fields ...*wool.LogField) {
	s.Wool.Focus(msg, fields...)
}

func (s *Base) LogForward(msg string, args ...any) {
	_, _ = s.Wool.Forward([]byte(fmt.Sprintf(msg, args...)))
}

func (s *Base) Version() *basev0.Version {
	return &basev0.Version{Version: s.Configuration.Version}
}

func (s *Base) Ready() {
	//	s.State = LoadState
}

func (s *Base) WantSync() {
	//s.State = SyncWantedState
}

func (s *Base) Stop() error {
	return nil
}

func (s *Base) Communicate(ctx context.Context, eng *agentv0.Engage) (*agentv0.InformationRequest, error) {
	s.Wool.Trace("base communicate: sending to server", wool.Field("eng", eng))
	return s.Communication.Communicate(ctx, eng)
}

type TemplateWrapper struct {
	dir shared.Dir
	fs  *shared.FSReader

	// Destination
	relative string
	absolute string

	PathSelect shared.PathSelect
	Override   shared.Override
}

func (wrapper *TemplateWrapper) WithPathSelect(pathSelect shared.PathSelect) *TemplateWrapper {
	wrapper.PathSelect = pathSelect
	return wrapper
}

func (wrapper *TemplateWrapper) WithOverride(override shared.Override) *TemplateWrapper {
	wrapper.Override = override
	return wrapper
}

func WithTemplate(fs embed.FS, from string, to string) *TemplateWrapper {
	return &TemplateWrapper{
		fs: shared.Embed(fs), dir: shared.NewDir("templates/%s", from), relative: to}
}

func (wrapper *TemplateWrapper) WithDestination(destination string) *TemplateWrapper {
	wrapper.absolute = destination
	return wrapper

}

func (wrapper *TemplateWrapper) Destination(s *Base) shared.Dir {
	if wrapper.absolute != "" {
		return shared.NewDir(wrapper.absolute)
	}
	return shared.NewDir(s.Local(wrapper.relative))

}

func (s *Base) Templates(ctx context.Context, obj any, wrappers ...*TemplateWrapper) error {
	for _, wrapper := range wrappers {
		templator := &templates.Templator{PathSelect: wrapper.PathSelect, Override: wrapper.Override}
		destination := wrapper.Destination(s)
		s.Wool.Trace("copying and applying template", wool.DirField(destination.Absolute()))
		err := templator.CopyAndApply(ctx, wrapper.fs, wrapper.dir, destination, obj)
		if err != nil {
			return s.Wool.Wrapf(err, "cannot copy and apply template")
		}
	}
	return nil
}
