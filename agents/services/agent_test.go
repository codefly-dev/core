package services

import (
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestServiceAgentExposesAdditiveExecutionExporterClient(t *testing.T) {
	connection, err := grpc.NewClient(
		"passthrough:///execution-exporter-client",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	agent := NewServiceAgentClient(connection)
	if agent.AgentClient == nil {
		t.Fatal("Agent client is nil")
	}
	if agent.ExecutionExporter == nil {
		t.Fatal("ExecutionExporter client is nil")
	}
}
