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
	agentsv1 "github.com/codefly-dev/core/proto/v1/go/agents"
	basev1 "github.com/codefly-dev/core/proto/v1/go/base"
	servicev1 "github.com/codefly-dev/core/proto/v1/go/services"
	factoryv1 "github.com/codefly-dev/core/proto/v1/go/services/factory"
	runtimev1 "github.com/codefly-dev/core/proto/v1/go/services/runtime"
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
}

type Base struct {
	// Agent
	Agent *configurations.Agent

	// State
	Identity              *servicev1.ServiceIdentity
	Location              string
	ConfigurationLocation string
	Configuration         *configurations.Service

	// Information convenience
	Information *Information

	// Endpoints
	Endpoints []*basev1.Endpoint

	// Runtime
	State InformationStatus

	// Loggers
	ServiceLogger *agents.ServiceLogger
	AgentLogger   *agents.AgentLogger

	// Communication Manager
	CommunicationClientManager *communicate.ClientManager

	// Code Watcher
	Watcher *code.Watcher
	Events  chan code.Change

	// Internal
	ctx context.Context
}

func NewServiceBase(agent *configurations.Agent) *Base {
	return &Base{
		Agent:                      agent,
		CommunicationClientManager: communicate.NewClientManager(),
	}
}

func (s *Base) Context() context.Context {
	return s.ctx
}

func (s *Base) Init(req *servicev1.InitRequest, settings any) error {

	s.Identity = req.Identity
	s.ServiceLogger = agents.NewServiceLogger(s.Identity, s.Agent)

	s.AgentLogger = agents.NewAgentLogger(s.Identity, s.Agent)
	defer s.AgentLogger.Catch()
	s.Location = req.Location

	s.ConfigurationLocation = path.Join(s.Location, "codefly")
	err := shared.CheckDirectoryOrCreate(s.ConfigurationLocation)

	if err != nil {
		return s.Wrapf(err, "cannot create configuration directory")
	}

	s.AgentLogger.Debugf("Location %v", s.Location)
	if req.Debug {
		s.AgentLogger.SetDebug() // For developers
	}

	ctx := context.Background()
	ctx = context.WithValue(ctx, shared.Agent, s.AgentLogger)
	ctx = context.WithValue(ctx, shared.Service, s.ServiceLogger)
	s.ctx = ctx

	s.Configuration, err = configurations.LoadFromDir[configurations.Service](s.Location)
	if err != nil {
		return s.Wrapf(err, "cannot load service configuration")
	}

	err = s.Configuration.LoadSettingsFromSpec(settings)
	if err != nil {
		return s.Wrapf(err, "cannot load settings from spec")
	}

	s.Information = &Information{
		Service: configurations.ToServiceWithCase(s.Configuration),
		Agent:   s.Agent,
	}
	s.CommunicationClientManager.WithLogger(s.AgentLogger)
	s.DebugMe("setup successful for %v", s.Identity)
	return nil
}

func (s *Base) Create(settings any, endpoints ...*basev1.Endpoint) (*factoryv1.CreateResponse, error) {
	err := s.Configuration.UpdateSpecFromSettings(settings)
	if err != nil {
		return nil, s.Wrapf(err, "cannot update spec")
	}
	err = s.Configuration.Save()
	if err != nil {
		return nil, s.Wrapf(err, "cannot save configuration")
	}
	return &factoryv1.CreateResponse{
		Endpoints: endpoints,
	}, nil
}

func (s *Base) FactoryInitResponseError(err error) (*factoryv1.InitResponse, error) {
	return &factoryv1.InitResponse{
		Status: &servicev1.InitStatus{State: servicev1.InitStatus_ERROR, Message: err.Error()},
	}, nil
}

func (s *Base) RuntimeInitResponse(endpoints []*basev1.Endpoint, channels ...*agentsv1.Channel) (*runtimev1.InitResponse, error) {
	// for convenience, add application and service
	for _, endpoint := range endpoints {
		endpoint.Application = s.Configuration.Application
		endpoint.Service = s.Configuration.Name
	}
	return &runtimev1.InitResponse{
		Version:   s.Version(),
		Endpoints: endpoints,
		Channels:  channels,
		Status:    &servicev1.InitStatus{State: servicev1.InitStatus_READY},
	}, nil
}

func (s *Base) RuntimeInitResponseError(err error) (*runtimev1.InitResponse, error) {
	return &runtimev1.InitResponse{
		Status: &servicev1.InitStatus{State: servicev1.InitStatus_ERROR, Message: err.Error()},
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
		if e.Api == configurations.Grpc {
			endpoint, err := endpoints.NewGrpcApi(e, s.Local("api.proto"))
			if err != nil {
				return nil, s.AgentLogger.Wrapf(err, "cannot create grpc api")
			}
			eps = append(eps, endpoint)
			continue
		}
		if e.Api == configurations.Rest {
			endpoint, err := endpoints.NewRestApiFromOpenAPI(s.Context(), e, s.Local("api.swagger.json"))
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

func (s *Base) Version() *servicev1.Version {
	return &servicev1.Version{Version: s.Configuration.Version}
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

type Channel struct {
	Method agentsv1.Method
	Client *communicate.ClientContext
}

func NewChannel(method agentsv1.Method, client *communicate.ClientContext) *Channel {
	return &Channel{Method: method, Client: client}
}

func NewDynamicChannel(method agentsv1.Method) *Channel {
	return &Channel{Method: method}
}

func (s *Base) WithCommunications(channels ...*Channel) ([]*agentsv1.Channel, error) {
	var out []*agentsv1.Channel
	for _, c := range channels {
		out = append(out, &agentsv1.Channel{Method: c.Method})
		if c.Client == nil {
			continue
		}
		err := s.CommunicationClientManager.Add(c.Method, c.Client)
		if err != nil {
			return nil, s.AgentLogger.Wrapf(err, "cannot add communication client")
		}
	}
	return out, nil
}

func (s *Base) Wire(method agentsv1.Method, client *communicate.ClientContext) error {
	return s.CommunicationClientManager.Add(method, client)
}

func (s *Base) Communicate(eng *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	if eng.Method == agentsv1.Method_UNKNOWN {
		return nil, s.AgentLogger.Errorf("unknown method")
	}
	s.AgentLogger.DebugMe("SENDING TO CLIENT MANAGER: %v", eng)
	return s.CommunicationClientManager.Process(eng)
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
	return TemplateWrapper{fs: shared.Embed(fs), dir: shared.NewDir("templates/deployment"), relative: "codefly/deployment"}
}

func WithDeploymentFor(fs embed.FS, relativePath string) TemplateWrapper {
	return TemplateWrapper{fs: shared.Embed(fs),
		dir:      shared.NewDir("templates/deployment/%s", relativePath),
		relative: fmt.Sprintf("codefly/deployment/%s", relativePath)}
}

func (s *Base) Templates(obj any, ws ...TemplateWrapper) error {
	s.AgentLogger.Debugf("templates: %v", s.Location)
	for _, w := range ws {
		ignore := templates.NewIgnore(w.ignores...)
		err := templates.CopyAndApply(s.AgentLogger, w.fs, w.dir, shared.NewDir(s.Local(w.relative)), obj, ignore)
		if err != nil {
			return s.AgentLogger.Wrapf(err, "cannot copy and apply template")
		}
	}
	return nil
}
