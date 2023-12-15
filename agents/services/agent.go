package services

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/configurations"
	v1agent "github.com/codefly-dev/core/generated/go/services/agent/v1"
	"github.com/codefly-dev/core/shared"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

type Agent interface {
	GetAgentInformation(ctx context.Context, req *v1agent.AgentInformationRequest) (*v1agent.AgentInformation, error)
}

type ServiceAgentContext struct {
}

func (m ServiceAgentContext) Key(p *configurations.Agent, unique string) string {
	return p.Key(configurations.ServiceAgent, unique)
}

func (m ServiceAgentContext) Default() plugin.Plugin {
	return &ServiceAgentGRPC{}
}

var _ agents.AgentContext = ServiceAgentContext{}

type ServiceAgent struct {
	client v1agent.AgentClient
	agent  *configurations.Agent
}

// GetAgentInformation provides
// - capabilities
func (m *ServiceAgent) GetAgentInformation(ctx context.Context, req *v1agent.AgentInformationRequest) (*v1agent.AgentInformation, error) {
	return m.client.GetAgentInformation(ctx, req)
}

type ServiceAgentGRPC struct {
	// GRPCAgent must still implement the ServiceAgent interface
	plugin.Plugin
	Service Agent
}

func (p *ServiceAgentGRPC) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	v1agent.RegisterAgentServer(s, &ServiceAgentServer{Service: p.Service})
	return nil
}

func (p *ServiceAgentGRPC) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &ServiceAgent{client: v1agent.NewAgentClient(c)}, nil
}

// ServiceAgentServer wraps the gRPC protocol Request/Response
type ServiceAgentServer struct {
	v1agent.UnimplementedAgentServer
	Service Agent
}

func (m *ServiceAgentServer) GetAgentInformation(ctx context.Context, req *v1agent.AgentInformationRequest) (*v1agent.AgentInformation, error) {
	return m.Service.GetAgentInformation(ctx, req)
}

func LoadAgent(ctx context.Context, service *configurations.Service) (*ServiceAgent, error) {
	if service == nil {
		return nil, fmt.Errorf("service cannot be nil")
	}
	logger := shared.NewLogger().With("services.LoadAgent<%s>", service.Name)
	if service.Agent == nil {
		return nil, logger.Errorf("agent cannot be nil")
	}
	agent, err := agents.Load[ServiceAgentContext, ServiceAgent](
		ctx,
		service.Agent.Of(configurations.ServiceAgent),
		service.Unique())
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load service agent")
	}
	agent.agent = service.Agent
	return agent, nil
}

func NewServiceAgent(conf *configurations.Agent, service Agent) agents.AgentImplementation {
	return agents.AgentImplementation{
		Configuration: conf,
		Agent:         &ServiceAgentGRPC{Service: service},
	}
}
