package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	agentv1 "example.test/independent-agent/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type server struct {
	agentv1.UnimplementedAgentServer
}

func (server) GetAgentInformation(context.Context, *agentv1.AgentInformationRequest) (*agentv1.AgentInformation, error) {
	declaration := &agentv1.AgentContract{ProtocolVersion: 1, StartupProtocolVersion: 1}
	switch os.Getenv("TEST_AGENT_CONTRACT") {
	case "unimplemented":
		return nil, status.Error(codes.Unimplemented, "agent information is unavailable")
	case "absent":
		declaration = nil
	case "future":
		declaration.ProtocolVersion = 2
	case "undeclared-startup":
		declaration.StartupProtocolVersion = 0
	case "future-startup":
		declaration.StartupProtocolVersion = 2
	}
	return &agentv1.AgentInformation{Contract: declaration}, nil
}

func main() {
	network, address := "tcp", "127.0.0.1:0"
	if socket := os.Getenv("CODEFLY_AGENT_UDS_PATH"); socket != "" {
		network, address = "unix", socket
	}
	listener, err := net.Listen(network, address)
	if err != nil {
		panic(err)
	}
	token := os.Getenv("CODEFLY_AGENT_TOKEN")
	rpc := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if info.FullMethod != healthv1.Health_Check_FullMethodName {
			md, _ := metadata.FromIncomingContext(ctx)
			values := md.Get("x-codefly-token")
			if token == "" || len(values) != 1 || values[0] != token {
				return nil, status.Error(codes.Unauthenticated, "invalid agent token")
			}
		}
		return handler(ctx, req)
	}))
	agentv1.RegisterAgentServer(rpc, server{})
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(rpc, healthServer)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() { <-ctx.Done(); rpc.GracefulStop() }()
	endpoint := "dns:///" + listener.Addr().String()
	if network == "unix" {
		endpoint = "unix:" + address
	}
	fmt.Printf("1|%s\n", endpoint)
	if err := rpc.Serve(listener); err != nil {
		panic(err)
	}
}
