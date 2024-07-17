package services

import (
	"context"
	"embed"
	"fmt"
	"path"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/resources"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/agents/communicate"
	"github.com/codefly-dev/core/agents/helpers/code"

	"github.com/codefly-dev/core/agents"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
)

type Information struct {
	Service *resources.ServiceWithCase
	Module  *resources.ModuleWithCase
	Agent   *resources.Agent
}

type Base struct {
	// Agent
	Agent *resources.Agent
	Wool  *wool.Wool

	// Service logger
	Logger *wool.Wool

	// Continuity check
	loaded bool

	// State
	Identity *resources.ServiceIdentity

	// Location of the service
	Location string

	// codefly configuration
	ConfigurationLocation string

	Service *resources.Service

	Environment *basev0.Environment

	// Information convenience
	Information *Information

	// Endpoints
	Endpoints           []*basev0.Endpoint
	DependencyEndpoints []*basev0.Endpoint

	NetworkMappings []*basev0.NetworkMapping

	// EnvironmentVariables
	EnvironmentVariables *resources.EnvironmentVariableManager

	// Configuration
	Configuration *basev0.Configuration

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

	// Docker Image for simple deployment convenience
	image *resources.DockerImage
}

func NewServiceBase(ctx context.Context, agent *resources.Agent) *Base {
	provider := agents.NewAgentProvider(ctx, agent)
	base := &Base{
		Agent:                agent,
		Communication:        communicate.NewServer(ctx),
		Wool:                 provider.Get(ctx),
		EnvironmentVariables: resources.NewEnvironmentVariableManager(),
	}
	base.Runtime = &RuntimeWrapper{Base: base}
	base.Builder = &BuilderWrapper{Base: base}
	return base
}

func (s *Base) Unique() string {
	return s.Identity.Unique()
}

func (s *Base) UniqueWithWorkspace() string {
	s.Wool.Debug("unique with workspace", wool.Field("workspace", s.Identity.Workspace))
	if s.Environment.NamingScope == "" {
		return s.Identity.UniqueWithWorkspace(s.Identity.Workspace)
	}
	return s.Identity.UniqueWithWorkspaceAndScope(s.Identity.Workspace, s.Environment.NamingScope)
}

func (s *Base) HeadlessLoad(ctx context.Context, identity *basev0.ServiceIdentity) error {
	// Information about what we run
	s.Identity = resources.ServiceIdentityFromProto(identity)
	s.Location = path.Join(identity.WorkspacePath, identity.RelativeToWorkspace)

	s.EnvironmentVariables = resources.NewEnvironmentVariableManager()

	agentProvider := agents.NewServiceAgentProvider(ctx, s.Identity)
	s.Wool = agentProvider.Get(ctx)

	serviceProvider := agents.NewServiceProvider(ctx, s.Identity)
	s.Logger = serviceProvider.Get(ctx)

	s.Wool.Debug("loading", wool.ServiceField(s.Identity.Name))

	s.Wool.Debug("loading service", wool.DirField(s.Location))

	s.Information = &Information{
		Agent: s.Agent,
	}

	s.loaded = true
	return nil
}

func (s *Base) Load(ctx context.Context, identity *basev0.ServiceIdentity, settings any) error {
	s.Identity = resources.ServiceIdentityFromProto(identity)

	s.Location = path.Join(identity.WorkspacePath, identity.RelativeToWorkspace)
	// Replace the Agent now that we know more!
	agentProvider := agents.NewServiceAgentProvider(ctx, s.Identity)
	serviceProvider := agents.NewServiceProvider(ctx, s.Identity)

	s.Wool = agentProvider.Get(ctx)
	s.Logger = serviceProvider.Get(ctx)

	ctx = s.Wool.Inject(ctx)

	var err error

	s.Service, err = resources.LoadServiceFromDir(ctx, s.Location)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot load service configuration")
	}

	s.Service.WithModule(s.Identity.Module)

	s.EnvironmentVariables = resources.NewEnvironmentVariableManager()

	s.EnvironmentVariables.SetIdentity(identity)

	err = s.Service.LoadSettingsFromSpec(settings)
	if err != nil {
		return s.Wool.Wrapf(err, "cannot load settings from spec")
	}

	s.Information = &Information{
		Service: resources.ToServiceWithCase(s.Identity),
		Module:  resources.ToModuleWithCase(s.Identity),
		Agent:   s.Agent,
	}

	s.loaded = true
	return nil
}

func (s *Base) SetDefaultDockerImage(req *builderv0.DockerBuildContext) {
	repo := req.DockerRepository
	s.image = &resources.DockerImage{
		Name: path.Join(repo, s.Identity.Module, s.Identity.Name),
		Tag:  s.Version().Version,
	}
}

func (s *Base) SetDockerImage(image *resources.DockerImage) {
	s.image = image
}

func (s *Base) DockerImage(req *builderv0.DockerBuildContext) *resources.DockerImage {
	if s.image == nil {
		s.SetDefaultDockerImage(req)
	}
	return s.image
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

func (s *Base) Local(f string, args ...any) string {
	return path.Join(s.Location, fmt.Sprintf(f, args...))
}

func (s *Base) LocalDirCreate(ctx context.Context, f string, args ...any) (string, error) {
	dir := path.Join(s.Location, fmt.Sprintf(f, args...))
	_, err := shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return "", s.Wool.Wrapf(err, "cannot create dir")
	}
	return dir, nil
}

/* Some very important helpers */

func (s *Base) Errorf(format string, args ...any) error {
	return s.Wool.NewError(format, args...)
}

func (s *Base) Infof(msg string, args ...any) {
	_, _ = s.Wool.Forward([]byte(fmt.Sprintf(msg, args...)))
}

func (s *Base) Version() *basev0.Version {
	if s == nil || s.Service == nil {
		return &basev0.Version{}
	}
	return &basev0.Version{Version: s.Service.Version}
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
	dir string
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
		fs: shared.Embed(fs), dir: fmt.Sprintf("templates/%s", from), relative: to}
}

func (wrapper *TemplateWrapper) WithDestination(destination string, args ...any) *TemplateWrapper {
	wrapper.absolute = fmt.Sprintf(destination, args...)
	return wrapper

}

func (wrapper *TemplateWrapper) Destination(s *Base) string {
	if wrapper.absolute != "" {
		return wrapper.absolute
	}
	return s.Local(wrapper.relative)

}

func (s *Base) Templates(ctx context.Context, obj any, wrappers ...*TemplateWrapper) error {
	for _, wrapper := range wrappers {
		templator := &templates.Templator{PathSelect: wrapper.PathSelect, Override: wrapper.Override, NameReplacer: templates.CutTemplateSuffix{}}
		destination := wrapper.Destination(s)
		s.Wool.Trace("copying and applying template", wool.DirField(destination))
		err := templator.CopyAndApply(ctx, wrapper.fs, wrapper.dir, destination, obj)
		if err != nil {
			return s.Wool.Wrapf(err, "cannot copy and apply template")
		}
	}
	return nil
}

func (s *Base) BaseEndpoint(api string) *resources.Endpoint {
	return s.Identity.BaseEndpoint(api)

}
