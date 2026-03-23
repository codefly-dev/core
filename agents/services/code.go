package services

import (
	"github.com/codefly-dev/core/agents/manager"
	"github.com/codefly-dev/core/resources"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"google.golang.org/grpc"
)

// CodeAgent is the client-side wrapper for the Code gRPC service.
type CodeAgent struct {
	codev0.CodeClient
	Agent       *resources.Agent
	ProcessInfo *manager.ProcessInfo
}

// NewCodeAgentClient creates a CodeAgent from an existing gRPC connection.
func NewCodeAgentClient(conn *grpc.ClientConn) *CodeAgent {
	return &CodeAgent{
		CodeClient: codev0.NewCodeClient(conn),
	}
}
