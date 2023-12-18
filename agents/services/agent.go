package services

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/configurations"
	agentv1 "github.com/codefly-dev/core/generated/go/services/agent/v1"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

type Agent interface {
	GetAgentInformation(ctx context.Context, req *agentv1.AgentInformationRequest) (*agentv1.AgentInformation, error)
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
	client agentv1.AgentClient
	agent  *configurations.Agent
}

// GetAgentInformation provides
// - capabilities
func (m *ServiceAgent) GetAgentInformation(ctx context.Context, req *agentv1.AgentInformationRequest) (*agentv1.AgentInformation, error) {
	return m.client.GetAgentInformation(ctx, req)
}

type ServiceAgentGRPC struct {
	// GRPCAgent must still implement the ServiceAgent interface
	plugin.Plugin
	Service Agent
}

func (p *ServiceAgentGRPC) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	agentv1.RegisterAgentServer(s, &ServiceAgentServer{Service: p.Service})
	return nil
}

func (p *ServiceAgentGRPC) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &ServiceAgent{client: agentv1.NewAgentClient(c)}, nil
}

// ServiceAgentServer wraps the gRPC protocol Request/Response
type ServiceAgentServer struct {
	agentv1.UnimplementedAgentServer
	Service Agent
}

func (m *ServiceAgentServer) GetAgentInformation(ctx context.Context, req *agentv1.AgentInformationRequest) (*agentv1.AgentInformation, error) {
	return m.Service.GetAgentInformation(ctx, req)
}

func LoadAgent(ctx context.Context, agent *configurations.Agent) (*ServiceAgent, error) {
	if agent == nil {
		return nil, fmt.Errorf("service cannot be nil")
	}
	w := wool.Get(ctx).In("services.LoadAgent", wool.Field("agent", agent.Name))
	w.Debug("loading service agent")
	if agent.Version == "latest" {
		err := agents.PinToLatestRelease(agent)
		if err != nil {
			return nil, w.Wrap(err)
		}
	}
	loaded, err := agents.Load[ServiceAgentContext, ServiceAgent](
		ctx,
		agent.Of(configurations.ServiceAgent),
		agent.Unique())
	if err != nil {
		return nil, w.Wrap(err)
	}
	loaded.agent = agent
	return loaded, nil
}

// NewServiceAgent binds the agent implementation to the agent
func NewServiceAgent(conf *configurations.Agent, service Agent) agents.AgentImplementation {
	return agents.AgentImplementation{
		Configuration: conf,
		Agent:         &ServiceAgentGRPC{Service: service},
	}
}
