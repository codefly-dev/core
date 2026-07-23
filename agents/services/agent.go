package services

import (
	"context"

	"github.com/codefly-dev/core/agents/manager"
	"github.com/codefly-dev/core/resources"

	executionv1 "github.com/codefly-dev/core/generated/go/codefly/execution/v1"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"google.golang.org/grpc"
)

// Agent is the Go interface that plugin types implement on the server side.
type Agent interface {
	GetAgentInformation(ctx context.Context, req *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error)
}

// ServiceAgent is the client-side wrapper for the Agent gRPC service.
type ServiceAgent struct {
	agentv0.AgentClient
	// ExecutionExporter is the additive product-neutral receipt exporter
	// capability. Callers must still require its advertised capability before
	// invoking it.
	ExecutionExporter executionv1.ExecutionExporterClient
	Agent             *resources.Agent
	ProcessInfo       *manager.ProcessInfo
}

// NewServiceAgentClient creates a ServiceAgent from an existing gRPC connection.
func NewServiceAgentClient(conn *grpc.ClientConn) *ServiceAgent {
	return &ServiceAgent{
		AgentClient:       agentv0.NewAgentClient(conn),
		ExecutionExporter: executionv1.NewExecutionExporterClient(conn),
	}
}
