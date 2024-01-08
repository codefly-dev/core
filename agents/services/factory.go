package services

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/agents/manager"

	"github.com/codefly-dev/core/agents/communicate"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/configurations"
	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	factoryv0 "github.com/codefly-dev/core/generated/go/services/factory/v0"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

type ServiceFactoryAgentContext struct {
}

func (m ServiceFactoryAgentContext) Key(p *configurations.Agent, unique string) string {
	return p.Key(configurations.FactoryServiceAgent, unique)
}

func (m ServiceFactoryAgentContext) Default() plugin.Plugin {
	return &FactoryAgentGRPC{}
}

var _ manager.AgentContext = ServiceFactoryAgentContext{}

type Factory interface {
	Load(ctx context.Context, req *factoryv0.LoadRequest) (*factoryv0.LoadResponse, error)
	Init(ctx context.Context, req *factoryv0.InitRequest) (*factoryv0.InitResponse, error)

	Create(ctx context.Context, req *factoryv0.CreateRequest) (*factoryv0.CreateResponse, error)
	Update(ctx context.Context, req *factoryv0.UpdateRequest) (*factoryv0.UpdateResponse, error)

	Sync(ctx context.Context, req *factoryv0.SyncRequest) (*factoryv0.SyncResponse, error)

	Build(ctx context.Context, req *factoryv0.BuildRequest) (*factoryv0.BuildResponse, error)
	Deploy(ctx context.Context, req *factoryv0.DeploymentRequest) (*factoryv0.DeploymentResponse, error)

	// Communicate is a special method that is used to communicate with the agent
	communicate.Communicate
}

type FactoryAgent struct {
	client  factoryv0.FactoryClient
	agent   *configurations.Agent
	process *manager.ProcessInfo
}

func (m FactoryAgent) Load(ctx context.Context, req *factoryv0.LoadRequest) (*factoryv0.LoadResponse, error) {
	return m.client.Load(ctx, req)
}

func (m FactoryAgent) Init(ctx context.Context, req *factoryv0.InitRequest) (*factoryv0.InitResponse, error) {
	return m.client.Init(ctx, req)
}

func (m FactoryAgent) Create(ctx context.Context, req *factoryv0.CreateRequest) (*factoryv0.CreateResponse, error) {
	return m.client.Create(ctx, req)
}

func (m FactoryAgent) Update(ctx context.Context, req *factoryv0.UpdateRequest) (*factoryv0.UpdateResponse, error) {
	return m.client.Update(ctx, req)
}

func (m FactoryAgent) Sync(ctx context.Context, req *factoryv0.SyncRequest) (*factoryv0.SyncResponse, error) {
	return m.client.Sync(ctx, req)
}

func (m FactoryAgent) Build(ctx context.Context, req *factoryv0.BuildRequest) (*factoryv0.BuildResponse, error) {
	return m.client.Build(ctx, req)
}

func (m FactoryAgent) Deploy(ctx context.Context, req *factoryv0.DeploymentRequest) (*factoryv0.DeploymentResponse, error) {
	return m.client.Deploy(ctx, req)
}

func (m FactoryAgent) Communicate(ctx context.Context, req *agentv0.Engage) (*agentv0.InformationRequest, error) {
	return m.client.Communicate(ctx, req)
}

type FactoryAgentGRPC struct {
	// GRPCAgent must still implement the ServiceAgent interface
	plugin.Plugin
	Factory Factory
}

func (p *FactoryAgentGRPC) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	factoryv0.RegisterFactoryServer(s, &FactoryServer{Factory: p.Factory})
	return nil
}

func (p *FactoryAgentGRPC) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &FactoryAgent{client: factoryv0.NewFactoryClient(c)}, nil
}

// FactoryServer wraps the gRPC protocol Request/Response
type FactoryServer struct {
	factoryv0.UnimplementedFactoryServer
	Factory Factory
}

func (m *FactoryServer) Load(ctx context.Context, req *factoryv0.LoadRequest) (*factoryv0.LoadResponse, error) {
	return m.Factory.Load(ctx, req)
}

func (m *FactoryServer) Init(ctx context.Context, req *factoryv0.InitRequest) (*factoryv0.InitResponse, error) {
	return m.Factory.Init(ctx, req)
}

func (m *FactoryServer) Create(ctx context.Context, req *factoryv0.CreateRequest) (*factoryv0.CreateResponse, error) {
	return m.Factory.Create(ctx, req)
}

func (m *FactoryServer) Update(ctx context.Context, req *factoryv0.UpdateRequest) (*factoryv0.UpdateResponse, error) {
	return m.Factory.Update(ctx, req)
}

func (m *FactoryServer) Sync(ctx context.Context, req *factoryv0.SyncRequest) (*factoryv0.SyncResponse, error) {
	return m.Factory.Sync(ctx, req)
}

func (m *FactoryServer) Build(ctx context.Context, req *factoryv0.BuildRequest) (*factoryv0.BuildResponse, error) {
	return m.Factory.Build(ctx, req)
}

func (m *FactoryServer) Deploy(ctx context.Context, req *factoryv0.DeploymentRequest) (*factoryv0.DeploymentResponse, error) {
	return m.Factory.Deploy(ctx, req)
}

func (m *FactoryServer) Communicate(ctx context.Context, req *agentv0.Engage) (*agentv0.InformationRequest, error) {
	return m.Factory.Communicate(ctx, req)
}

func LoadFactory(ctx context.Context, conf *configurations.Service) (*FactoryAgent, error) {
	w := wool.Get(ctx).In("services.LoadFactory", wool.ThisField(conf))
	if conf == nil {
		return nil, fmt.Errorf("conf cannot be nil")
	}
	if conf.Agent == nil {
		return nil, w.NewError("agent cannot be nil")
	}
	factory, process, err := manager.Load[ServiceFactoryAgentContext, FactoryAgent](ctx, conf.Agent.Of(configurations.FactoryServiceAgent), conf.Unique())
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service factory conf")
	}
	factory.agent = conf.Agent
	factory.process = process
	return factory, nil
}

func NewFactoryAgent(conf *configurations.Agent, factory Factory) agents.AgentImplementation {
	return agents.AgentImplementation{
		Configuration: conf,
		Agent:         &FactoryAgentGRPC{Factory: factory},
	}
}
