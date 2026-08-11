package grpcconfig

import (
	"context"
	"net"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	toolingv0 "github.com/codefly-dev/core/generated/go/codefly/services/tooling/v0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type envelopeToolingServer struct {
	toolingv0.UnimplementedToolingServer
	payloadBytes int
}

func (s envelopeToolingServer) GetSemanticIndex(context.Context, *toolingv0.GetSemanticIndexRequest) (*toolingv0.GetSemanticIndexResponse, error) {
	return &toolingv0.GetSemanticIndexResponse{Failure: &basev0.Failure{
		Code:    basev0.FailureCode_FAILURE_CODE_INTERNAL,
		Message: strings.Repeat("x", s.payloadBytes),
	}}, nil
}

func TestTypedMessageEnvelopeTransportsPayloadAboveGRPCDefault(t *testing.T) {
	client := startEnvelopeTooling(t, MaxTypedMessageBytes, 5<<20)
	response, err := client.GetSemanticIndex(t.Context(), &toolingv0.GetSemanticIndexRequest{})
	if err != nil {
		t.Fatalf("receive typed payload above gRPC default: %v", err)
	}
	if got := len(response.GetFailure().GetMessage()); got != 5<<20 {
		t.Fatalf("received payload bytes = %d, want %d", got, 5<<20)
	}
}

func TestTypedMessageEnvelopeFailsClosedAboveLimit(t *testing.T) {
	const limit = 1 << 20
	client := startEnvelopeTooling(t, limit, 2<<20)
	_, err := client.GetSemanticIndex(t.Context(), &toolingv0.GetSemanticIndexRequest{})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("over-limit call error = %v, want ResourceExhausted", err)
	}
}

func startEnvelopeTooling(t *testing.T, maxBytes, payloadBytes int) toolingv0.ToolingClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer(typedMessageServerOptions(maxBytes)...)
	toolingv0.RegisterToolingServer(server, envelopeToolingServer{payloadBytes: payloadBytes})
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	connection, err := grpc.NewClient(
		"passthrough:///"+listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		typedMessageClientDialOption(maxBytes),
	)
	if err != nil {
		t.Fatalf("create gRPC client: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return toolingv0.NewToolingClient(connection)
}
