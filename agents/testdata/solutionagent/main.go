package main

import (
	"context"

	"github.com/codefly-dev/core/agents"
	solutionv1 "github.com/codefly-dev/core/generated/go/codefly/services/solution/v1"
)

// server implements a single Solution RPC returning a sentinel so the
// registration test can distinguish "Serve registered Solution and routed
// to this handler" from gRPC's Unimplemented (which the embedded
// UnimplementedSolutionServer would also return for an unregistered service).
type server struct {
	solutionv1.UnimplementedSolutionServer
}

func (server) GetSolutionInformation(context.Context, *solutionv1.GetSolutionInformationRequest) (*solutionv1.GetSolutionInformationResponse, error) {
	return &solutionv1.GetSolutionInformationResponse{
		Artifact: &solutionv1.SolutionArtifact{Name: "solution-fixture"},
	}, nil
}

func main() {
	agents.Serve(agents.PluginRegistration{Solution: server{}})
}
