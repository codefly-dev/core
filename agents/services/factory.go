package services

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/agents/manager"

	"github.com/codefly-dev/core/agents/communicate"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/configurations"
	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
	factoryv1 "github.com/codefly-dev/core/generated/go/services/factory/v1"
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
	Init(ctx context.Context, req *factoryv1.InitRequest) (*factoryv1.InitResponse, error)

	Create(ctx context.Context, req *factoryv1.CreateRequest) (*factoryv1.CreateResponse, error)
	Update(ctx context.Context, req *factoryv1.UpdateRequest) (*factoryv1.UpdateResponse, error)

	Sync(ctx context.Context, req *factoryv1.SyncRequest) (*factoryv1.SyncResponse, error)

	Build(ctx context.Context, req *factoryv1.BuildRequest) (*factoryv1.BuildResponse, error)
	Deploy(ctx context.Context, req *factoryv1.DeploymentRequest) (*factoryv1.DeploymentResponse, error)

	// Communicate is a special method that is used to communicate with the agent
	communicate.Communicate
}

type FactoryAgent struct {
	client factoryv1.FactoryClient
	agent  *configurations.Agent
}

func (m FactoryAgent) Init(ctx context.Context, req *factoryv1.InitRequest) (*factoryv1.InitResponse, error) {
	return m.client.Init(ctx, req)
}

func (m FactoryAgent) Create(ctx context.Context, req *factoryv1.CreateRequest) (*factoryv1.CreateResponse, error) {
	return m.client.Create(ctx, req)
}

func (m FactoryAgent) Update(ctx context.Context, req *factoryv1.UpdateRequest) (*factoryv1.UpdateResponse, error) {
	return m.client.Update(ctx, req)
}

func (m FactoryAgent) Sync(ctx context.Context, req *factoryv1.SyncRequest) (*factoryv1.SyncResponse, error) {
	return m.client.Sync(ctx, req)
}

func (m FactoryAgent) Build(ctx context.Context, req *factoryv1.BuildRequest) (*factoryv1.BuildResponse, error) {
	return m.client.Build(ctx, req)
}

func (m FactoryAgent) Deploy(ctx context.Context, req *factoryv1.DeploymentRequest) (*factoryv1.DeploymentResponse, error) {
	return m.client.Deploy(ctx, req)
}

func (m FactoryAgent) Communicate(ctx context.Context, req *agentv1.Engage) (*agentv1.InformationRequest, error) {
	return m.client.Communicate(ctx, req)
}

type FactoryAgentGRPC struct {
	// GRPCAgent must still implement the ServiceAgent interface
	plugin.Plugin
	Factory Factory
}

func (p *FactoryAgentGRPC) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	factoryv1.RegisterFactoryServer(s, &FactoryServer{Factory: p.Factory})
	return nil
}

func (p *FactoryAgentGRPC) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &FactoryAgent{client: factoryv1.NewFactoryClient(c)}, nil
}

// FactoryServer wraps the gRPC protocol Request/Response
type FactoryServer struct {
	factoryv1.UnimplementedFactoryServer
	Factory Factory
}

func (m *FactoryServer) Init(ctx context.Context, req *factoryv1.InitRequest) (*factoryv1.InitResponse, error) {
	return m.Factory.Init(ctx, req)
}

func (m *FactoryServer) Create(ctx context.Context, req *factoryv1.CreateRequest) (*factoryv1.CreateResponse, error) {
	return m.Factory.Create(ctx, req)
}

func (m *FactoryServer) Update(ctx context.Context, req *factoryv1.UpdateRequest) (*factoryv1.UpdateResponse, error) {
	return m.Factory.Update(ctx, req)
}

func (m *FactoryServer) Sync(ctx context.Context, req *factoryv1.SyncRequest) (*factoryv1.SyncResponse, error) {
	return m.Factory.Sync(ctx, req)
}

func (m *FactoryServer) Build(ctx context.Context, req *factoryv1.BuildRequest) (*factoryv1.BuildResponse, error) {
	return m.Factory.Build(ctx, req)
}

func (m *FactoryServer) Deploy(ctx context.Context, req *factoryv1.DeploymentRequest) (*factoryv1.DeploymentResponse, error) {
	return m.Factory.Deploy(ctx, req)
}

func (m *FactoryServer) Communicate(ctx context.Context, req *agentv1.Engage) (*agentv1.InformationRequest, error) {
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
	factory, err := manager.Load[ServiceFactoryAgentContext, FactoryAgent](ctx, conf.Agent.Of(configurations.FactoryServiceAgent), conf.Unique())
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service factory conf")
	}
	factory.agent = conf.Agent
	return factory, nil
}

func NewFactoryAgent(conf *configurations.Agent, factory Factory) agents.AgentImplementation {
	return agents.AgentImplementation{
		Configuration: conf,
		Agent:         &FactoryAgentGRPC{Factory: factory},
	}
}
