package services

import (
	"github.com/codefly-dev/core/agents/manager"
	"github.com/codefly-dev/core/resources"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"google.golang.org/grpc"
)

// BuilderAgent is the client-side wrapper for the Builder gRPC service.
type BuilderAgent struct {
	builderv0.BuilderClient
	Agent       *resources.Agent
	ProcessInfo *manager.ProcessInfo
}

// NewBuilderAgentClient creates a BuilderAgent from an existing gRPC connection.
func NewBuilderAgentClient(conn *grpc.ClientConn) *BuilderAgent {
	return &BuilderAgent{
		BuilderClient: builderv0.NewBuilderClient(conn),
	}
}
