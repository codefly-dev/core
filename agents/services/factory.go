package services

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/configurations"
	agentsv1 "github.com/codefly-dev/core/proto/v1/go/agents"
	servicev1 "github.com/codefly-dev/core/proto/v1/go/services"
	factoryv1 "github.com/codefly-dev/core/proto/v1/go/services/factory"
	"github.com/codefly-dev/core/shared"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

type IFactory interface {
	Init(req *servicev1.InitRequest) (*factoryv1.InitResponse, error)

	Create(req *factoryv1.CreateRequest) (*factoryv1.CreateResponse, error)
	Update(req *factoryv1.UpdateRequest) (*factoryv1.UpdateResponse, error)

	Sync(req *factoryv1.SyncRequest) (*factoryv1.SyncResponse, error)

	Build(req *factoryv1.BuildRequest) (*factoryv1.BuildResponse, error)
	Deploy(req *factoryv1.DeploymentRequest) (*factoryv1.DeploymentResponse, error)

	Communicate(req *agentsv1.Engage) (*agentsv1.InformationRequest, error)
}

type ServiceFactory struct {
	client factoryv1.FactoryClient
	agent  *configurations.Agent
}

type ServiceFactoryAgentContext struct {
}

func (m ServiceFactoryAgentContext) Key(p *configurations.Agent, unique string) string {
	return p.Key(configurations.AgentFactoryService, unique)
}

func (m ServiceFactoryAgentContext) Default() plugin.Plugin {
	return &ServiceFactoryAgent{}
}

func (m ServiceFactory) Init(ctx context.Context, req *servicev1.InitRequest) (*factoryv1.InitResponse, error) {
	return m.client.Init(ctx, req)
}

func (m ServiceFactory) Create(ctx context.Context, req *factoryv1.CreateRequest) (*factoryv1.CreateResponse, error) {
	return m.client.Create(ctx, req)
}

func (m ServiceFactory) Update(ctx context.Context, req *factoryv1.UpdateRequest) (*factoryv1.UpdateResponse, error) {
	return m.client.Update(ctx, req)
}

func (m ServiceFactory) Sync(ctx context.Context, req *factoryv1.SyncRequest) (*factoryv1.SyncResponse, error) {
	return m.client.Sync(ctx, req)
}

func (m ServiceFactory) Build(ctx context.Context, req *factoryv1.BuildRequest) (*factoryv1.BuildResponse, error) {
	return m.client.Build(ctx, req)
}

func (m ServiceFactory) Deploy(ctx context.Context, req *factoryv1.DeploymentRequest) (*factoryv1.DeploymentResponse, error) {
	return m.client.Deploy(ctx, req)
}

func (m ServiceFactory) Communicate(ctx context.Context, req *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	return m.client.Communicate(ctx, req)
}

type ServiceFactoryAgent struct {
	// GRPCAgent must still implement the Agent interface
	plugin.Plugin
	Factory IFactory
}

func (p *ServiceFactoryAgent) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	factoryv1.RegisterFactoryServer(s, &FactoryServer{Factory: p.Factory})
	return nil
}

func (p *ServiceFactoryAgent) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &ServiceFactory{client: factoryv1.NewFactoryClient(c)}, nil
}

// FactoryServer wraps the gRPC protocol Request/Response
type FactoryServer struct {
	factoryv1.UnimplementedFactoryServer
	Factory IFactory
}

func (m *FactoryServer) Init(_ context.Context, req *servicev1.InitRequest) (*factoryv1.InitResponse, error) {
	return m.Factory.Init(req)
}

func (m *FactoryServer) Create(_ context.Context, req *factoryv1.CreateRequest) (*factoryv1.CreateResponse, error) {
	return m.Factory.Create(req)
}

func (m *FactoryServer) Update(_ context.Context, req *factoryv1.UpdateRequest) (*factoryv1.UpdateResponse, error) {
	return m.Factory.Update(req)
}

func (m *FactoryServer) Sync(_ context.Context, req *factoryv1.SyncRequest) (*factoryv1.SyncResponse, error) {
	return m.Factory.Sync(req)
}

func (m *FactoryServer) Build(_ context.Context, req *factoryv1.BuildRequest) (*factoryv1.BuildResponse, error) {
	return m.Factory.Build(req)
}

func (m *FactoryServer) Deploy(_ context.Context, req *factoryv1.DeploymentRequest) (*factoryv1.DeploymentResponse, error) {
	return m.Factory.Deploy(req)
}

func (m *FactoryServer) Communicate(_ context.Context, req *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	return m.Factory.Communicate(req)
}

func LoadFactory(ctx context.Context, conf *configurations.Service) (*ServiceFactory, error) {
	if conf == nil {
		return nil, fmt.Errorf("conf cannot be nil")
	}
	if conf.Agent == nil {
		return nil, shared.NewLogger().With("services.LoadFactory<%s>", conf.Name).Errorf("agent found nil")
	}
	logger := shared.NewLogger().With("services.LoadFactory<%s>", conf.Agent.Identifier())
	logger.Debugf("loading service factory")
	factory, err := agents.Load[ServiceFactoryAgentContext, ServiceFactory](ctx, conf.Agent.Of(configurations.AgentFactoryService), conf.Unique())
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load service factory conf")
	}
	factory.agent = conf.Agent
	return factory, nil
}

func NewFactoryAgent(conf *configurations.Agent, factory IFactory) agents.AgentImplementation {
	return agents.AgentImplementation{
		Configuration: conf,
		Agent:         &ServiceFactoryAgent{Factory: factory},
	}
}
