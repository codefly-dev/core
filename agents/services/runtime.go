package services

import (
	"context"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/agents/manager"

	"github.com/codefly-dev/core/agents/communicate"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/configurations"
	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/services/runtime/v0"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

type ServiceRuntimeAgentContext struct {
}

func (m ServiceRuntimeAgentContext) Key(p *configurations.Agent, unique string) string {
	return p.Key(configurations.RuntimeServiceAgent, unique)
}

func (m ServiceRuntimeAgentContext) Default() plugin.Plugin {
	return &RuntimeAgentGRPC{}
}

var _ manager.AgentContext = ServiceRuntimeAgentContext{}

type Runtime interface {
	// Load loads the service: it is a NoOp operation and can be called safely
	Load(ctx context.Context, req *runtimev0.LoadRequest) (*runtimev0.LoadResponse, error)

	// Init initializes the service: can include steps like compilation, etc...
	Init(ctx context.Context, req *runtimev0.InitRequest) (*runtimev0.InitResponse, error)

	// Start the underlying service
	Start(ctx context.Context, req *runtimev0.StartRequest) (*runtimev0.StartResponse, error)

	Stop(ctx context.Context, req *runtimev0.StopRequest) (*runtimev0.StopResponse, error)

	Information(ctx context.Context, req *runtimev0.InformationRequest) (*runtimev0.InformationResponse, error)

	// Communicate is a special method that is used to communicate with the agent
	communicate.Communicate
}

type RuntimeAgent struct {
	client  runtimev0.RuntimeClient
	agent   *configurations.Agent
	process *manager.ProcessInfo
}

// Load loads the service: it is a NoOp operation and can be called safely
func (m *RuntimeAgent) Load(ctx context.Context, req *runtimev0.LoadRequest) (*runtimev0.LoadResponse, error) {
	return m.client.Load(ctx, req)
}

// Init initializes the service
func (m *RuntimeAgent) Init(ctx context.Context, req *runtimev0.InitRequest) (*runtimev0.InitResponse, error) {
	return m.client.Init(ctx, req)
}

// Start starts the service
func (m *RuntimeAgent) Start(ctx context.Context, req *runtimev0.StartRequest) (*runtimev0.StartResponse, error) {
	return m.client.Start(ctx, req)
}

// Information return some useful information about the service
func (m *RuntimeAgent) Information(ctx context.Context, req *runtimev0.InformationRequest) (*runtimev0.InformationResponse, error) {
	return m.client.Information(ctx, req)
}

// Stop stops the service
func (m *RuntimeAgent) Stop(ctx context.Context, req *runtimev0.StopRequest) (*runtimev0.StopResponse, error) {
	return m.client.Stop(ctx, req)
}

// Communicate helper
func (m *RuntimeAgent) Communicate(ctx context.Context, req *agentv0.Engage) (*agentv0.InformationRequest, error) {
	return m.client.Communicate(ctx, req)
}

type RuntimeAgentGRPC struct {
	// GRPCAgent must still implement the ServiceAgent interface
	plugin.Plugin
	Runtime Runtime
}

func (p *RuntimeAgentGRPC) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	runtimev0.RegisterRuntimeServer(s, &RuntimeServer{Runtime: p.Runtime})
	return nil
}

func (p *RuntimeAgentGRPC) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &RuntimeAgent{client: runtimev0.NewRuntimeClient(c)}, nil
}

// RuntimeServer wraps the gRPC protocol Request/Response
type RuntimeServer struct {
	runtimev0.UnimplementedRuntimeServer
	Runtime Runtime
}

func (m *RuntimeServer) Load(ctx context.Context, req *runtimev0.LoadRequest) (*runtimev0.LoadResponse, error) {
	return m.Runtime.Load(ctx, req)
}

func (m *RuntimeServer) Init(ctx context.Context, req *runtimev0.InitRequest) (*runtimev0.InitResponse, error) {
	return m.Runtime.Init(ctx, req)
}

func (m *RuntimeServer) Start(ctx context.Context, req *runtimev0.StartRequest) (*runtimev0.StartResponse, error) {
	return m.Runtime.Start(ctx, req)
}

func (m *RuntimeServer) Information(ctx context.Context, req *runtimev0.InformationRequest) (*runtimev0.InformationResponse, error) {
	return m.Runtime.Information(ctx, req)
}

func (m *RuntimeServer) Stop(ctx context.Context, req *runtimev0.StopRequest) (*runtimev0.StopResponse, error) {
	return m.Runtime.Stop(ctx, req)
}

func (m *RuntimeServer) Communicate(ctx context.Context, req *agentv0.Engage) (*agentv0.InformationRequest, error) {
	return m.Runtime.Communicate(ctx, req)
}

/*
Loader
*/

func LoadRuntime(ctx context.Context, service *configurations.Service) (*RuntimeAgent, error) {
	w := wool.Get(ctx).In("services.LoadRuntime", wool.ThisField(service))
	if service == nil || service.Agent == nil {
		return nil, w.NewError("agent cannot be nil")
	}
	runtime, process, err := manager.Load[ServiceRuntimeAgentContext, RuntimeAgent](
		ctx,
		service.Agent.Of(configurations.RuntimeServiceAgent),
		service.Unique())
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service runtime agent")
	}
	runtime.agent = service.Agent
	runtime.process = process
	return runtime, nil
}

func NewRuntimeAgent(conf *configurations.Agent, runtime Runtime) agents.AgentImplementation {
	return agents.AgentImplementation{
		Configuration: conf,
		Agent:         &RuntimeAgentGRPC{Runtime: runtime},
	}
}

type InformationStatus = runtimev0.InformationResponse_Status

const (
	UnknownState    = runtimev0.InformationResponse_UNKNOWN
	LoadInProgress  = runtimev0.InformationResponse_LOAD_IN_PROGRESS
	LoadSuccess     = runtimev0.InformationResponse_LOADED_SUCCESS
	LoadFailed      = runtimev0.InformationResponse_LOADED_FAILED
	InitInProgress  = runtimev0.InformationResponse_INIT_IN_PROGRESS
	InitSuccess     = runtimev0.InformationResponse_INIT_SUCCESS
	InitFailed      = runtimev0.InformationResponse_INIT_FAILED
	StartInProgress = runtimev0.InformationResponse_START_IN_PROGRESS
	StartSuccess    = runtimev0.InformationResponse_START_SUCCESS
	StartFailed     = runtimev0.InformationResponse_START_FAILED
	StopInProgress  = runtimev0.InformationResponse_STOP_IN_PROGRESS
	StopSuccess     = runtimev0.InformationResponse_STOP_SUCCESS
	StopFailed      = runtimev0.InformationResponse_STOP_FAILED
)

type InformationStateDesired = runtimev0.InformationResponse_DesiredState

const (
	DesiredNOOP    = runtimev0.InformationResponse_NOOP
	DesiredRestart = runtimev0.InformationResponse_RESTARTED
	DesiredStop    = runtimev0.InformationResponse_STOPPED
)
