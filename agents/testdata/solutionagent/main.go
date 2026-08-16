package main

import (
	"context"

	"github.com/codefly-dev/core/agents"
	solutionv0 "github.com/codefly-dev/core/generated/go/codefly/services/solution/v0"
)

// server implements a single Solution RPC returning a sentinel so the
// registration test can distinguish "Serve registered Solution and routed
// to this handler" from gRPC's Unimplemented (which the embedded
// UnimplementedSolutionServer would also return for an unregistered service).
type server struct {
	solutionv0.UnimplementedSolutionServer
}

func (server) GetSolutionInformation(context.Context, *solutionv0.GetSolutionInformationRequest) (*solutionv0.GetSolutionInformationResponse, error) {
	return &solutionv0.GetSolutionInformationResponse{
		Artifact: &solutionv0.SolutionArtifact{Name: "solution-fixture"},
	}, nil
}

func main() {
	agents.Serve(agents.PluginRegistration{Solution: server{}})
}
