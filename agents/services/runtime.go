package services

import (
	"context"
	"fmt"
	"github.com/codefly-dev/core/agents/communicate"
	"strings"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/configurations"
	agentsv1 "github.com/codefly-dev/core/proto/v1/go/agents"
	v1 "github.com/codefly-dev/core/proto/v1/go/services"
	runtimev1 "github.com/codefly-dev/core/proto/v1/go/services/runtime"
	"github.com/codefly-dev/core/shared"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

type ServiceRuntimeAgentContext struct {
}

func (m ServiceRuntimeAgentContext) Key(p *configurations.Agent, unique string) string {
	return p.Key(configurations.RuntimeServiceAgent, unique)
}

func (m ServiceRuntimeAgentContext) Default() plugin.Plugin {
	return &RuntimeAgentGRPC{}
}

var _ agents.AgentContext = ServiceRuntimeAgentContext{}

type Runtime interface {
	Init(ctx context.Context, req *v1.InitRequest) (*runtimev1.InitResponse, error)

	Configure(ctx context.Context, req *runtimev1.ConfigureRequest) (*runtimev1.ConfigureResponse, error)

	Start(ctx context.Context, req *runtimev1.StartRequest) (*runtimev1.StartResponse, error)
	Information(ctx context.Context, req *runtimev1.InformationRequest) (*runtimev1.InformationResponse, error)

	Stop(ctx context.Context, req *runtimev1.StopRequest) (*runtimev1.StopResponse, error)

	// Communicate is a special method that is used to communicate with the agent
	communicate.Communicate
}

type RuntimeAgent struct {
	client runtimev1.RuntimeClient
	agent  *configurations.Agent
}

// Configure documents things
// It can be used safely anywhere: doesn't start or do anything
func (m *RuntimeAgent) Configure(ctx context.Context, req *runtimev1.ConfigureRequest) (*runtimev1.ConfigureResponse, error) {
	return m.client.Configure(ctx, req)
}

// Init initializes the service
func (m *RuntimeAgent) Init(ctx context.Context, req *v1.InitRequest) (*runtimev1.InitResponse, error) {
	resp, err := m.client.Init(ctx, req)
	if err != nil && strings.Contains(err.Error(), "Marshal called with nil") {
		return resp, fmt.Errorf("WE PROBABLY HAVE A PANIC")
	}
	return resp, err
}

// Start starts the service
func (m *RuntimeAgent) Start(ctx context.Context, req *runtimev1.StartRequest) (*runtimev1.StartResponse, error) {
	resp, err := m.client.Start(ctx, req)
	if err != nil {
		st := status.Convert(err)
		for _, detail := range st.Details() {
			switch t := detail.(type) {
			case *errdetails.DebugInfo:
				return nil, shared.ParseError(t.Detail)
			}
		}
	}
	return resp, err
}

// Information return some useful information about the service
func (m *RuntimeAgent) Information(ctx context.Context, req *runtimev1.InformationRequest) (*runtimev1.InformationResponse, error) {
	return m.client.Information(ctx, req)
}

// Stop stops the service
func (m *RuntimeAgent) Stop(ctx context.Context, req *runtimev1.StopRequest) (*runtimev1.StopResponse, error) {
	return m.client.Stop(ctx, req)
}

// Communicate helper
func (m *RuntimeAgent) Communicate(ctx context.Context, req *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	return m.client.Communicate(ctx, req)
}

type RuntimeAgentGRPC struct {
	// GRPCAgent must still implement the ServiceAgent interface
	plugin.Plugin
	Runtime Runtime
}

func (p *RuntimeAgentGRPC) GRPCServer(ctx *plugin.GRPCBroker, s *grpc.Server) error {
	runtimev1.RegisterRuntimeServer(s, &RuntimeServer{Runtime: p.Runtime})
	return nil
}

func (p *RuntimeAgentGRPC) GRPCClient(ctx context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &RuntimeAgent{client: runtimev1.NewRuntimeClient(c)}, nil
}

// RuntimeServer wraps the gRPC protocol Request/Response
type RuntimeServer struct {
	runtimev1.UnimplementedRuntimeServer
	Runtime Runtime
}

func (m *RuntimeServer) Configure(ctx context.Context, req *runtimev1.ConfigureRequest) (*runtimev1.ConfigureResponse, error) {
	return m.Runtime.Configure(ctx, req)
}

func (m *RuntimeServer) Init(ctx context.Context, req *v1.InitRequest) (*runtimev1.InitResponse, error) {
	return m.Runtime.Init(ctx, req)
}

func (m *RuntimeServer) Start(ctx context.Context, req *runtimev1.StartRequest) (*runtimev1.StartResponse, error) {
	return m.Runtime.Start(ctx, req)
}

func (m *RuntimeServer) Information(ctx context.Context, req *runtimev1.InformationRequest) (*runtimev1.InformationResponse, error) {
	return m.Runtime.Information(ctx, req)
}

func (m *RuntimeServer) Stop(ctx context.Context, req *runtimev1.StopRequest) (*runtimev1.StopResponse, error) {
	return m.Runtime.Stop(ctx, req)
}

func (m *RuntimeServer) Communicate(ctx context.Context, req *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	return m.Runtime.Communicate(ctx, req)
}

/*
Loader
*/

func LoadRuntime(ctx context.Context, service *configurations.Service) (*RuntimeAgent, error) {
	logger := shared.NewLogger().With("services.LoadRuntime")
	if service == nil || service.Agent == nil {
		return nil, logger.Errorf("agent cannot be nil")
	}
	runtime, err := agents.Load[ServiceRuntimeAgentContext, RuntimeAgent](
		ctx,
		service.Agent.Of(configurations.RuntimeServiceAgent),
		service.Unique())
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load service runtime agent")
	}
	runtime.agent = service.Agent
	return runtime, nil
}

func NewRuntimeAgent(conf *configurations.Agent, runtime Runtime) agents.AgentImplementation {
	return agents.AgentImplementation{
		Configuration: conf,
		Agent:         &RuntimeAgentGRPC{Runtime: runtime},
	}
}

type InformationStatus = runtimev1.InformationResponse_Status

const (
	UnknownState       = runtimev1.InformationResponse_UNKNOWN
	InitState          = runtimev1.InformationResponse_INIT
	StartedState       = runtimev1.InformationResponse_STARTED
	RestartWantedState = runtimev1.InformationResponse_RESTART_WANTED
	SyncWantedState    = runtimev1.InformationResponse_SYNC_WANTED
	StoppedState       = runtimev1.InformationResponse_STOPPED
	ErrorState         = runtimev1.InformationResponse_ERROR
)
