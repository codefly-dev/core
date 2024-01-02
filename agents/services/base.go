package services

import (
	"context"
	"embed"
	"fmt"
	"path"

	"github.com/codefly-dev/core/agents/network"

	"github.com/codefly-dev/core/configurations/standards"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/agents/communicate"
	"github.com/codefly-dev/core/agents/helpers/code"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/configurations"
	basev1 "github.com/codefly-dev/core/generated/go/base/v1"
	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
	factoryv1 "github.com/codefly-dev/core/generated/go/services/factory/v1"
	runtimev1 "github.com/codefly-dev/core/generated/go/services/runtime/v1"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
)

type Information struct {
	Service *configurations.ServiceWithCase
	Agent   *configurations.Agent
	Domain  string
}

type RuntimeWrapper struct {
	*Base
}

type FactoryWrapper struct {
	*Base
}

type Base struct {
	// Agent
	Agent     *configurations.Agent
	WoolAgent *wool.Provider
	Wool      *wool.Wool

	// Underlying service
	WoolService *wool.Provider

	// State
	Identity *configurations.ServiceIdentity
	Location string

	// codefly configuration
	ConfigurationLocation string

	Configuration *configurations.Service

	// Information convenience
	Information *Information

	// Endpoints
	Endpoints       []*basev1.Endpoint
	NetworkMappings []*runtimev1.NetworkMapping

	// Wrappers
	Runtime *RuntimeWrapper
	Factory *FactoryWrapper

	// Runtime
	State        InformationStatus
	DesiredState InformationStateDesired

	// Communication
	Communication *communicate.Server

	// Code Watcher
	Watcher *code.Watcher
	Events  chan code.Change
}

func NewServiceBase(ctx context.Context, agent *configurations.Agent) *Base {
	provider := agents.NewAgentProvider(ctx, agent)
	base := &Base{
		Agent:         agent,
		Communication: communicate.NewServer(ctx),
		WoolAgent:     provider,
		Wool:          provider.Get(ctx),
	}
	base.Runtime = &RuntimeWrapper{Base: base}
	base.Factory = &FactoryWrapper{Base: base}
	return base
}

func (s *Base) Load(ctx context.Context, identity *basev1.ServiceIdentity, settings any) error {
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
	return nil
}

func (s *Base) DockerImage() *configurations.DockerImage {
	return &configurations.DockerImage{
		Name: fmt.Sprintf("%s/%s", s.Identity.Application, s.Identity.Name),
		Tag:  s.Version().Version,
	}
}

func (s *FactoryWrapper) LoadResponse(es []*basev1.Endpoint, gettingStarted string) (*factoryv1.LoadResponse, error) {
	for _, e := range es {
		e.Application = s.Identity.Application
		e.Service = s.Identity.Name
		e.Namespace = s.Identity.Namespace
	}
	return &factoryv1.LoadResponse{
		Version:        s.Version(),
		Endpoints:      es,
		GettingStarted: gettingStarted,
		Status:         &factoryv1.LoadStatus{State: factoryv1.LoadStatus_READY},
	}, nil
}

func (s *FactoryWrapper) LoadError(err error) (*factoryv1.LoadResponse, error) {
	return &factoryv1.LoadResponse{
		Status: &factoryv1.LoadStatus{State: factoryv1.LoadStatus_ERROR, Message: err.Error()},
	}, err
}

func (s *FactoryWrapper) InitResponse() (*factoryv1.InitResponse, error) {
	return &factoryv1.InitResponse{}, nil
}

func (s *FactoryWrapper) CreateResponse(ctx context.Context, settings any, endpoints ...*basev1.Endpoint) (*factoryv1.CreateResponse, error) {
	err := s.Configuration.UpdateSpecFromSettings(settings)
	if err != nil {
		return s.CreateError(err)
	}
	s.Configuration.Endpoints, err = configurations.FromProtoEndpoints(endpoints...)
	if err != nil {
		return s.CreateError(err)
	}

	err = s.Configuration.Save(ctx)
	if err != nil {
		return nil, s.Wool.Wrapf(err, "base: cannot save configuration")
	}
	return &factoryv1.CreateResponse{
		Endpoints: endpoints,
	}, nil
}

func (s *FactoryWrapper) CreateError(err error) (*factoryv1.CreateResponse, error) {
	return &factoryv1.CreateResponse{
		Status: &factoryv1.CreateStatus{Status: factoryv1.CreateStatus_ERROR, Message: err.Error()},
	}, err
}

// Runtime

func (s *RuntimeWrapper) LoadResponse(endpoints []*basev1.Endpoint) (*runtimev1.LoadResponse, error) {
	s.Wool.Debug("load response", wool.NullableField("exposing endpoints", configurations.MakeEndpointSummary(endpoints)))
	// for convenience, add application and service
	for _, endpoint := range endpoints {
		endpoint.Application = s.Configuration.Application
		endpoint.Service = s.Configuration.Name
	}
	return &runtimev1.LoadResponse{
		Version:   s.Version(),
		Endpoints: endpoints,
		Status:    &runtimev1.LoadStatus{State: runtimev1.LoadStatus_READY},
	}, nil
}

func (s *RuntimeWrapper) LoadError(err error) (*runtimev1.LoadResponse, error) {
	return &runtimev1.LoadResponse{
		Status: &runtimev1.LoadStatus{State: runtimev1.LoadStatus_ERROR, Message: err.Error()},
	}, err
}

func (s *RuntimeWrapper) InitResponse() (*runtimev1.InitResponse, error) {
	s.Wool.Debug("init response", wool.NullableField("exposing network mappings", network.MakeNetworkMappingSummary(s.NetworkMappings)))
	return &runtimev1.InitResponse{
		Status:          &runtimev1.InitStatus{State: runtimev1.InitStatus_READY},
		NetworkMappings: s.NetworkMappings,
	}, nil
}

func (s *RuntimeWrapper) InitError(err error, fields ...*wool.LogField) (*runtimev1.InitResponse, error) {
	message := wool.Log{Message: err.Error(), Fields: fields}
	s.Wool.Error(err.Error(), fields...)
	return &runtimev1.InitResponse{
		Status: &runtimev1.InitStatus{State: runtimev1.InitStatus_ERROR, Message: message.String()},
	}, err
}

func (s *RuntimeWrapper) StartResponse() (*runtimev1.StartResponse, error) {
	return &runtimev1.StartResponse{
		Status: &runtimev1.StartStatus{State: runtimev1.StartStatus_STARTED},
	}, nil
}

func (s *RuntimeWrapper) StartError(err error, fields ...*wool.LogField) (*runtimev1.StartResponse, error) {
	message := wool.Log{Message: err.Error(), Fields: fields}
	s.Wool.Error(err.Error(), fields...)
	return &runtimev1.StartResponse{
		Status: &runtimev1.StartStatus{State: runtimev1.StartStatus_ERROR, Message: message.String()},
	}, err
}

func (s *RuntimeWrapper) InformationResponse(_ context.Context, _ *runtimev1.InformationRequest) (*runtimev1.InformationResponse, error) {
	resp := &runtimev1.InformationResponse{
		Status:       s.State,
		DesiredState: s.DesiredState,
	}
	// only send the restart information once
	if s.DesiredState == DesiredRestart {
		s.DesiredState = DesiredNOOP
	}
	return resp, nil
}

// EndpointsFromConfiguration from Configuration and data from the service
func (s *Base) EndpointsFromConfiguration(ctx context.Context) ([]*basev1.Endpoint, error) {
	var eps []*basev1.Endpoint
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

type WatchConfiguration struct {
	Includes []string
	Excludes []string
}

func NewWatchConfiguration(includes []string, excludes ...string) *WatchConfiguration {
	return &WatchConfiguration{
		Includes: includes,
		Excludes: excludes,
	}
}

func (s *Base) SetupWatcher(ctx context.Context, conf *WatchConfiguration, handler func(event code.Change) error) error {
	s.Wool.Debug("watching for changes")
	s.Events = make(chan code.Change)
	var err error
	s.Watcher, err = code.NewWatcher(ctx, s.Events, s.Location, conf.Includes, conf.Excludes...)
	if err != nil {
		return err
	}
	go s.Watcher.Start()

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

/* Helpers

 */

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

func (s *Base) Version() *basev1.Version {
	return &basev1.Version{Version: s.Configuration.Version}
}

func (s *Base) Ready() {
	//	s.State = LoadState
}

func (s *Base) WantRestart() {
	s.DesiredState = DesiredRestart
}

func (s *Base) WantSync() {
	//s.State = SyncWantedState
}

func (s *Base) Stop() error {
	return nil
}

func (s *Base) Communicate(ctx context.Context, eng *agentv1.Engage) (*agentv1.InformationRequest, error) {
	return s.Communication.Communicate(ctx, eng)
}

type TemplateWrapper struct {
	dir      shared.Dir
	fs       *shared.FSReader
	relative string
	ignores  []string
}

func WithFactory(fs embed.FS, ignores ...string) TemplateWrapper {
	return TemplateWrapper{fs: shared.Embed(fs), dir: shared.NewDir("templates/factory"), ignores: ignores}
}

func WithBuilder(fs embed.FS) TemplateWrapper {
	return TemplateWrapper{fs: shared.Embed(fs), dir: shared.NewDir("templates/builder"), relative: "codefly/builder"}
}

func WithDeployment(fs embed.FS) TemplateWrapper {
	return TemplateWrapper{
		fs: shared.Embed(fs), dir: shared.NewDir("templates/deployment"), relative: "codefly/deployment"}
}

//
//func WithDestination(destination string, args ...any) templates.TemplateOptionFunc {
//	return func(opt *templates.TemplateOption) {
//		opt.EndpointDestination = fmt.Sprintf(destination, args...)
//	}
//}
//
//func WithDeploymentFor(fs embed.FS, relativePath string, opts ...templates.TemplateOptionFunc) TemplateWrapper {
//	opt := templates.Option(relativePath, opts...)
//	return TemplateWrapper{
//		opts:     opts,
//		fs:       shared.Embed(fs),
//		dir:      shared.NewDir("templates/deployment/%s", relativePath),
//		relative: fmt.Sprintf("codefly/deployment/%s", opt.EndpointDestination)}
//}

func (s *Base) Templates(ctx context.Context, obj any, ws ...TemplateWrapper) error {
	s.Wool.Debug("templates")
	for _, w := range ws {
		err := templates.CopyAndApply(ctx, w.fs, w.dir, shared.NewDir(s.Local(w.relative)), obj)
		if err != nil {
			return s.Wool.Wrapf(err, "cannot copy and apply template")
		}
	}
	return nil
}
