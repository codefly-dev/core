package agents

import (
	"testing"

	solutionv0 "github.com/codefly-dev/core/generated/go/codefly/services/solution/v0"
	"google.golang.org/grpc"
)

const solutionServiceName = "codefly.services.solution.v0.Solution"

func TestRegisterServices_RegistersSolutionWhenSet(t *testing.T) {
	s := grpc.NewServer()
	registerServices(s, PluginRegistration{Solution: &solutionv0.UnimplementedSolutionServer{}})

	if _, ok := s.GetServiceInfo()[solutionServiceName]; !ok {
		t.Fatalf("Solution server not registered; got services %v", s.GetServiceInfo())
	}
}

func TestRegisterServices_SkipsSolutionWhenNil(t *testing.T) {
	s := grpc.NewServer()
	registerServices(s, PluginRegistration{})

	if _, ok := s.GetServiceInfo()[solutionServiceName]; ok {
		t.Fatal("Solution server registered despite nil PluginRegistration.Solution")
	}
}
