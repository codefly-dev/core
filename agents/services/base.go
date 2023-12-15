package services

import (
	"context"
	"embed"
	"fmt"
	"path"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/communicate"
	"github.com/codefly-dev/core/agents/endpoints"
	"github.com/codefly-dev/core/agents/helpers/code"

	"github.com/codefly-dev/core/configurations"
	basev1 "github.com/codefly-dev/core/generated/go/base/v1"
	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
	factoryv1 "github.com/codefly-dev/core/generated/go/services/factory/v1"
	runtimev1 "github.com/codefly-dev/core/generated/go/services/runtime/v1"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
)

func AgentLogger(ctx context.Context) *agents.AgentLogger {
	return ctx.Value(shared.Agent).(*agents.AgentLogger)
}

func ServiceLogger(ctx context.Context) *agents.ServiceLogger {
	return ctx.Value(shared.Service).(*agents.ServiceLogger)
}

type Information struct {
	Service *configurations.ServiceWithCase
	Agent   *configurations.Agent
	Domain  string
}

type Base struct {
	// Agent
	Agent *configurations.Agent

	// State
	Identity *basev1.ServiceIdentity
	Location string

	// codefly configuration
	ConfigurationLocation string

	Configuration *configurations.Service

	// Information convenience
	Information *Information

	// Endpoints
	Endpoints []*basev1.Endpoint

	// Runtime
	State InformationStatus

	// Loggers
	ServiceLogger *agents.ServiceLogger
	AgentLogger   *agents.AgentLogger

	// Communication
	Communication *communicate.Server

	// Code Watcher
	Watcher *code.Watcher
	Events  chan code.Change

	ctx context.Context
}

func NewServiceBase(ctx context.Context, agent *configurations.Agent) *Base {
	return &Base{
		Agent:         agent,
		Communication: communicate.NewServer(ctx),
	}
}

func (s *Base) Context() context.Context {
	return s.ctx
}

func (s *Base) Init(identity *basev1.ServiceIdentity, settings any) error {

	s.Identity = identity
	s.ServiceLogger = agents.NewServiceLogger(s.Identity, s.Agent)

	s.AgentLogger = agents.NewAgentLogger(s.Identity, s.Agent)
	defer s.AgentLogger.Catch()

	ctx := context.Background()
	ctx = context.WithValue(ctx, shared.Base, s.AgentLogger)
	ctx = context.WithValue(ctx, shared.Agent, s.AgentLogger)
	ctx = context.WithValue(ctx, shared.Service, s.ServiceLogger)
	s.ctx = ctx

	s.Location = identity.Location

	s.ConfigurationLocation = path.Join(s.Location, "codefly")
	err := shared.CheckDirectoryOrCreate(s.ctx, s.ConfigurationLocation)

	if err != nil {
		return s.Wrapf(err, "cannot create configuration directory")
	}

	s.AgentLogger.Debugf("Location %v", s.Location)

	s.Configuration, err = configurations.LoadServiceFromDirUnsafe(s.ctx, s.Location)
	if err != nil {
		return s.Wrapf(err, "cannot load service configuration")
	}

	err = s.Configuration.LoadSettingsFromSpec(settings)
	if err != nil {
		return s.Wrapf(err, "cannot load settings from spec")
	}

	s.Information = &Information{
		Service: configurations.ToServiceWithCase(s.Configuration),
		Domain:  s.Identity.Domain,
		Agent:   s.Agent,
	}
	s.AgentLogger.Debugf("setup successful for %v", s.Identity)
	return nil
}

func (s *Base) DockerImage() *configurations.DockerImage {
	s.AgentLogger.TODO("deal with the registry: provider")
	return &configurations.DockerImage{
		Name: fmt.Sprintf("%s/%s", s.Identity.Application, s.Identity.Name),
		Tag:  s.Version().Version,
	}
}

func (s *Base) CreateResponse(ctx context.Context, settings any, eps ...*basev1.Endpoint) (*factoryv1.CreateResponse, error) {
	err := s.Configuration.UpdateSpecFromSettings(settings)
	if err != nil {
		return s.CreateResponseError(err)
	}
	s.Configuration.Endpoints, err = endpoints.FromProtoEndpoints(eps...)
	if err != nil {
		return s.CreateResponseError(err)
	}

	err = s.Configuration.Save(ctx)
	if err != nil {
		return nil, s.Wrapf(err, "base: cannot save configuration")
	}
	return &factoryv1.CreateResponse{
		Endpoints: eps,
	}, nil
}

// Factory

func (s *Base) FactoryInitResponse(es []*basev1.Endpoint, readme string) (*factoryv1.InitResponse, error) {
	defer s.AgentLogger.Catch()
	for _, e := range es {
		e.Application = s.Identity.Application
		e.Service = s.Identity.Name
		e.Namespace = s.Identity.Namespace
	}
	return &factoryv1.InitResponse{
		Version:   s.Version(),
		Endpoints: es,
		ReadMe:    readme,
		Status:    &factoryv1.InitStatus{State: factoryv1.InitStatus_READY},
	}, nil
}

func (s *Base) FactoryInitResponseError(err error) (*factoryv1.InitResponse, error) {
	return &factoryv1.InitResponse{
		Status: &factoryv1.InitStatus{State: factoryv1.InitStatus_ERROR, Message: err.Error()},
	}, nil
}

func (s *Base) CreateResponseError(err error) (*factoryv1.CreateResponse, error) {
	return &factoryv1.CreateResponse{
		Status: &factoryv1.CreateStatus{Status: factoryv1.CreateStatus_ERROR, Message: err.Error()},
	}, nil
}

func (s *Base) RuntimeInitResponse(endpoints []*basev1.Endpoint) (*runtimev1.InitResponse, error) {
	// for convenience, add application and service
	for _, endpoint := range endpoints {
		endpoint.Application = s.Configuration.Application
		endpoint.Service = s.Configuration.Name
	}
	return &runtimev1.InitResponse{
		Version:   s.Version(),
		Endpoints: endpoints,
		Status:    &runtimev1.InitStatus{State: runtimev1.InitStatus_READY},
	}, nil
}

func (s *Base) RuntimeInitResponseError(err error) (*runtimev1.InitResponse, error) {
	return &runtimev1.InitResponse{
		Status: &runtimev1.InitStatus{State: runtimev1.InitStatus_ERROR, Message: err.Error()},
	}, nil
}

/* Some very important helpers */

func (s *Base) Wrapf(err error, format string, args ...interface{}) error {
	return s.AgentLogger.Wrapf(err, format, args...)
}

func (s *Base) Errorf(format string, args ...interface{}) error {
	return s.AgentLogger.Errorf(format, args...)
}

// EndpointsFromConfiguration from Configuration and data from the service
func (s *Base) EndpointsFromConfiguration() ([]*basev1.Endpoint, error) {
	var eps []*basev1.Endpoint
	for _, e := range s.Configuration.Endpoints {
		if e.API == configurations.Grpc {
			endpoint, err := endpoints.NewGrpcAPI(e, s.Local("api.proto"))
			if err != nil {
				return nil, s.AgentLogger.Wrapf(err, "cannot create grpc api")
			}
			eps = append(eps, endpoint)
			continue
		}
		if e.API == configurations.Rest {
			endpoint, err := endpoints.NewRestAPIFromOpenAPI(s.Context(), e, s.Local("api.swagger.json"))
			if err != nil {
				return nil, s.AgentLogger.Wrapf(err, "cannot create grpc api")
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

func (s *Base) SetupWatcher(conf *WatchConfiguration, handler func(event code.Change) error) error {
	s.AgentLogger.Debugf("watching for changes")
	s.Events = make(chan code.Change)
	var err error
	s.Watcher, err = code.NewWatcher(s.AgentLogger, s.Events, s.Location, conf.Includes, conf.Excludes...)
	if err != nil {
		return err
	}
	go s.Watcher.Start()

	go func() {
		for event := range s.Events {
			err := handler(event)
			if err != nil {
				s.AgentLogger.Debugf("OOPS: %v", err)
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

func (s *Base) DebugMe(format string, args ...any) {
	s.AgentLogger.DebugMe(format, args...)
}

func (s *Base) Debugf(format string, args ...any) {
	s.AgentLogger.Debugf(format, args...)
}

func ConfigureError(err error) *runtimev1.ConfigureStatus {
	return &runtimev1.ConfigureStatus{
		State:   runtimev1.ConfigureStatus_ERROR,
		Message: err.Error(),
	}
}

func ConfigureSuccess() *runtimev1.ConfigureStatus {
	return &runtimev1.ConfigureStatus{
		State: runtimev1.ConfigureStatus_READY,
	}
}

func StartError(err error) *runtimev1.StartStatus {
	return &runtimev1.StartStatus{
		State:   runtimev1.StartStatus_ERROR,
		Message: err.Error(),
	}
}

func StartSuccess() *runtimev1.StartStatus {
	return &runtimev1.StartStatus{
		State: runtimev1.StartStatus_STARTED,
	}
}

func (s *Base) Version() *basev1.Version {
	return &basev1.Version{Version: s.Configuration.Version}
}

func (s *Base) WantRestart() {
	s.State = RestartWantedState
}

func (s *Base) WantSync() {
	s.State = SyncWantedState
}

func (s *Base) Stop() error {
	s.State = StoppedState
	close(s.Events)
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
//		opt.Destination = fmt.Sprintf(destination, args...)
//	}
//}
//
//func WithDeploymentFor(fs embed.FS, relativePath string, opts ...templates.TemplateOptionFunc) TemplateWrapper {
//	opt := templates.Option(relativePath, opts...)
//	return TemplateWrapper{
//		opts:     opts,
//		fs:       shared.Embed(fs),
//		dir:      shared.NewDir("templates/deployment/%s", relativePath),
//		relative: fmt.Sprintf("codefly/deployment/%s", opt.Destination)}
//}

func (s *Base) Templates(ctx context.Context, obj any, ws ...TemplateWrapper) error {
	s.AgentLogger.Debugf("templates: %v", s.Location)
	for _, w := range ws {
		err := templates.CopyAndApply(ctx, w.fs, w.dir, shared.NewDir(s.Local(w.relative)), obj)
		if err != nil {
			return s.AgentLogger.Wrapf(err, "cannot copy and apply template")
		}
	}
	return nil
}
