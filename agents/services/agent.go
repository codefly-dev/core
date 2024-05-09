package services

import (
	"context"

	"github.com/codefly-dev/core/agents/manager"
	"github.com/codefly-dev/core/resources"

	"github.com/codefly-dev/core/agents"
	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

type Agent interface {
	GetAgentInformation(ctx context.Context, req *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error)
}

type ServiceAgentContext struct {
}

func (m ServiceAgentContext) Key(p *resources.Agent, unique string) string {
	return p.Key(resources.ServiceAgent, unique)
}

func (m ServiceAgentContext) Default() plugin.Plugin {
	return &ServiceAgentGRPC{}
}

var _ manager.AgentContext = ServiceAgentContext{}

type ServiceAgent struct {
	Client      agentv0.AgentClient
	Agent       *resources.Agent
	ProcessInfo *manager.ProcessInfo
}

// GetAgentInformation provides
// - capabilities
func (m *ServiceAgent) GetAgentInformation(ctx context.Context, req *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error) {
	return m.Client.GetAgentInformation(ctx, req)
}

type ServiceAgentGRPC struct {
	// GRPCAgent must still implement the ServiceAgent interface
	plugin.Plugin
	Service Agent
}

func (p *ServiceAgentGRPC) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	agentv0.RegisterAgentServer(s, &ServiceAgentServer{Service: p.Service})
	return nil
}

func (p *ServiceAgentGRPC) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &ServiceAgent{Client: agentv0.NewAgentClient(c)}, nil
}

// ServiceAgentServer wraps the gRPC protocol Request/Response
type ServiceAgentServer struct {
	agentv0.UnimplementedAgentServer
	Service Agent
}

func (m *ServiceAgentServer) GetAgentInformation(ctx context.Context, req *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error) {
	return m.Service.GetAgentInformation(ctx, req)
}

// NewServiceAgent binds the Agent implementation to the Agent
func NewServiceAgent(conf *resources.Agent, service Agent) agents.AgentImplementation {
	return agents.AgentImplementation{
		Configuration: conf,
		Agent:         &ServiceAgentGRPC{Service: service},
	}
}
