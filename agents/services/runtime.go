package services

import (
	"github.com/codefly-dev/core/agents/manager"
	"github.com/codefly-dev/core/resources"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"google.golang.org/grpc"
)

// RuntimeAgent is the client-side wrapper for the Runtime gRPC service.
type RuntimeAgent struct {
	runtimev0.RuntimeClient
	Agent       *resources.Agent
	ProcessInfo *manager.ProcessInfo

	// Some services support re-init without restarting.
	HotReload bool
}

// NewRuntimeAgentClient creates a RuntimeAgent from an existing gRPC connection.
func NewRuntimeAgentClient(conn *grpc.ClientConn) *RuntimeAgent {
	return &RuntimeAgent{
		RuntimeClient: runtimev0.NewRuntimeClient(conn),
	}
}
