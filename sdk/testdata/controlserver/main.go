// Command controlserver is a stand-in codefly CLI for the SDK's dependency
// session tests. It binds the control socket the SDK hands it, answers the
// session handshake with the secret it was started with, and records every RPC
// it serves so a test can prove which calls did — and did not — reach it.
//
// FAKE_CONTROL_MODE selects the misbehavior to exercise:
//
//	""             bind the socket and answer the handshake correctly
//	"no-socket"    ignore the socket and stay alive, like a CLI that predates
//	               the isolated session contract
//	"no-handshake" bind the socket but leave SessionHandshake unimplemented
//	"foreign"      bind the socket and answer with a different session secret
//	"wrong-version" answer a correct proof under a different protocol version
//	"no-capability" answer correctly but advertise no isolation capability
//	"hang-handshake" serve health as SERVING, then never answer the handshake
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	v0 "github.com/codefly-dev/core/generated/go/codefly/cli/v0"
	"github.com/codefly-dev/core/sdk/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

func main() {
	mode := os.Getenv("FAKE_CONTROL_MODE")
	if mode == "no-socket" {
		time.Sleep(10 * time.Minute)
		return
	}
	socket := os.Getenv(session.SocketEnvironment)
	if socket == "" {
		fmt.Fprintln(os.Stderr, "controlserver: no control socket was provided")
		os.Exit(2)
	}
	owner := session.FromEnvironment(os.Environ())
	if owner == nil {
		fmt.Fprintln(os.Stderr, "controlserver: no session identity was provided")
		os.Exit(2)
	}
	if mode == "foreign" {
		foreign, err := session.New()
		if err != nil {
			fmt.Fprintf(os.Stderr, "controlserver: %v\n", err)
			os.Exit(2)
		}
		owner = foreign
	}

	listener, err := net.Listen("unix", socket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "controlserver: listen on %s: %v\n", socket, err)
		os.Exit(2)
	}

	server := &controlServer{
		owner:      owner,
		stall:      mode == "hang-handshake",
		handshake:  mode != "no-handshake",
		version:    handshakeVersion(mode),
		capability: mode != "no-capability",
		record:     recordPath(socket),
	}
	// Identity first, so a test running two independent driver processes can
	// compare what each child was actually handed without reaching into the
	// SDK's unexported state.
	server.note(fmt.Sprintf("identity session=%s socket=%s scope=%s",
		session.FromEnvironment(os.Environ()).ID, socket, namingScope(os.Args)))

	srv := grpc.NewServer()
	v0.RegisterCLIServer(srv, server)
	checker := health.NewServer()
	checker.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, checker)
	if err := srv.Serve(listener); err != nil {
		fmt.Fprintf(os.Stderr, "controlserver: serve: %v\n", err)
		os.Exit(2)
	}
}

// recordPath keeps the RPC log outside the SDK-owned control directory, which
// the SDK deletes when it tears a session down, and names it after that
// directory so concurrent sessions never share one file.
func recordPath(socket string) string {
	dir := os.Getenv("FAKE_CONTROL_RECORD_DIR")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, filepath.Base(filepath.Dir(socket))+".log")
}

// namingScope reports the --naming-scope the SDK passed on the command line.
func namingScope(args []string) string {
	for i, arg := range args {
		if arg == "--naming-scope" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// handshakeVersion lets a fixture claim a protocol the SDK does not speak, so
// version skew can be told apart from a failed ownership proof.
func handshakeVersion(mode string) uint32 {
	if mode == "wrong-version" {
		return session.ProtocolVersion + 1
	}
	return session.ProtocolVersion
}

type controlServer struct {
	v0.UnimplementedCLIServer
	owner      *session.Session
	handshake  bool
	stall      bool
	version    uint32
	capability bool
	record     string
}

func (s *controlServer) note(name string) {
	if s.record == "" {
		return
	}
	file, err := os.OpenFile(s.record, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = fmt.Fprintln(file, name)
}

func (s *controlServer) SessionHandshake(ctx context.Context, req *v0.SessionHandshakeRequest) (*v0.SessionHandshakeResponse, error) {
	s.note("SessionHandshake")
	if s.stall {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if !s.handshake {
		return s.UnimplementedCLIServer.SessionHandshake(context.Background(), req)
	}
	var capabilities []string
	if s.capability {
		capabilities = append(capabilities, session.IsolatedControlSocketCapability)
	}
	return &v0.SessionHandshakeResponse{
		SessionId:       s.owner.ID,
		Proof:           s.owner.Proof(req.GetChallenge()),
		ProtocolVersion: s.version,
		Capabilities:    capabilities,
	}, nil
}

func (s *controlServer) Ping(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
	s.note("Ping")
	return &emptypb.Empty{}, nil
}

func (s *controlServer) GetFlowStatus(context.Context, *emptypb.Empty) (*v0.FlowStatus, error) {
	s.note("GetFlowStatus")
	return &v0.FlowStatus{Ready: true}, nil
}

func (s *controlServer) GetDependenciesNetworkMappings(context.Context, *v0.GetNetworkMappingsRequest) (*v0.GetNetworkMappingsResponse, error) {
	s.note("GetDependenciesNetworkMappings")
	return &v0.GetNetworkMappingsResponse{}, nil
}

func (s *controlServer) GetConfiguration(context.Context, *v0.GetConfigurationRequest) (*v0.GetConfigurationResponse, error) {
	s.note("GetConfiguration")
	return &v0.GetConfigurationResponse{}, nil
}

func (s *controlServer) GetDependenciesConfigurations(context.Context, *v0.GetConfigurationRequest) (*v0.GetConfigurationsResponse, error) {
	s.note("GetDependenciesConfigurations")
	return &v0.GetConfigurationsResponse{}, nil
}

func (s *controlServer) StopFlow(context.Context, *v0.StopFlowRequest) (*v0.StopFlowResponse, error) {
	s.note("StopFlow")
	return &v0.StopFlowResponse{}, nil
}

func (s *controlServer) DestroyFlow(context.Context, *v0.DestroyFlowRequest) (*v0.DestroyFlowResponse, error) {
	s.note("DestroyFlow")
	return &v0.DestroyFlowResponse{}, nil
}
