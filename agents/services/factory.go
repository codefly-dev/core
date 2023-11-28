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

func (m ServiceFactory) Init(req *servicev1.InitRequest) (*factoryv1.InitResponse, error) {
	return m.client.Init(context.Background(), req)
}

func (m ServiceFactory) Create(req *factoryv1.CreateRequest) (*factoryv1.CreateResponse, error) {
	return m.client.Create(context.Background(), req)
}

func (m ServiceFactory) Update(req *factoryv1.UpdateRequest) (*factoryv1.UpdateResponse, error) {
	return m.client.Update(context.Background(), req)
}

func (m ServiceFactory) Sync(req *factoryv1.SyncRequest) (*factoryv1.SyncResponse, error) {
	return m.client.Sync(context.Background(), req)
}

func (m ServiceFactory) Build(req *factoryv1.BuildRequest) (*factoryv1.BuildResponse, error) {
	return m.client.Build(context.Background(), req)
}

func (m ServiceFactory) Deploy(req *factoryv1.DeploymentRequest) (*factoryv1.DeploymentResponse, error) {
	return m.client.Deploy(context.Background(), req)
}

func (m ServiceFactory) Communicate(req *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	return m.client.Communicate(context.Background(), req)
}

type ServiceFactoryAgent struct {
	// GRPCAgent must still implement the Agent interface
	plugin.Plugin
	Factory IFactory
}

func (p *ServiceFactoryAgent) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	factoryv1.RegisterFactoryServer(s, &FactoryServer{Factory: p.Factory})
	return nil
}

func (p *ServiceFactoryAgent) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &ServiceFactory{client: factoryv1.NewFactoryClient(c)}, nil
}

// FactoryServer wraps the gRPC protocol Request/Response
type FactoryServer struct {
	factoryv1.UnimplementedFactoryServer
	Factory IFactory
}

func (m *FactoryServer) Init(ctx context.Context, req *servicev1.InitRequest) (*factoryv1.InitResponse, error) {
	return m.Factory.Init(req)
}

func (m *FactoryServer) Create(ctx context.Context, req *factoryv1.CreateRequest) (*factoryv1.CreateResponse, error) {
	return m.Factory.Create(req)
}

func (m *FactoryServer) Update(ctx context.Context, req *factoryv1.UpdateRequest) (*factoryv1.UpdateResponse, error) {
	return m.Factory.Update(req)
}

func (m *FactoryServer) Sync(ctx context.Context, req *factoryv1.SyncRequest) (*factoryv1.SyncResponse, error) {
	return m.Factory.Sync(req)
}

func (m *FactoryServer) Build(ctx context.Context, req *factoryv1.BuildRequest) (*factoryv1.BuildResponse, error) {
	return m.Factory.Build(req)
}

func (m *FactoryServer) Deploy(ctx context.Context, req *factoryv1.DeploymentRequest) (*factoryv1.DeploymentResponse, error) {
	return m.Factory.Deploy(req)
}

func (m *FactoryServer) Communicate(ctx context.Context, req *agentsv1.Engage) (*agentsv1.InformationRequest, error) {
	return m.Factory.Communicate(req)
}

func LoadFactory(conf *configurations.Service) (*ServiceFactory, error) {
	if conf == nil {
		return nil, fmt.Errorf("conf cannot be nil")
	}
	if conf.Agent == nil {
		return nil, shared.NewLogger("services.LoadFactory<%s>", conf.Name).Errorf("agent found nil")
	}
	logger := shared.NewLogger("services.LoadFactory<%s>", conf.Agent.Name())
	logger.Debugf("loading service factory")
	factory, err := agents.Load[ServiceFactoryAgentContext, ServiceFactory](conf.Agent.Of(configurations.AgentFactoryService), conf.Unique())
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
