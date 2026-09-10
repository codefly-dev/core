// Command testcli is a Codefly control server for the SDK session tests. It
// speaks the real CLI gRPC contract over a real socket and publishes a real TCP
// dependency, so a test can start it through the ordinary WithDependencies
// spawn path and then connect to the endpoint the session resolved.
//
// It lives under testdata so it stays out of the library's build graph — it is
// not part of core's public surface and has no business in `go build ./...`,
// `go vet ./...`, the coverage denominator or the CGO-free guard. The session
// tests compile it explicitly, which is what keeps it from rotting.
//
// Everything it serves is derived from its working directory: the SDK runs it
// in the directory the session is anchored to, so two concurrent sessions get
// two distinct identities and two distinct endpoints without any shared state.
package main

import (
	"context"
	"fmt"
	"net"
	"os"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	v0 "github.com/codefly-dev/core/generated/go/codefly/cli/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/sdk/session"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

// failEnvironment names an RPC that must fail, so a test can force a real
// resolution failure in the middle of the dependency handshake.
const failEnvironment = "CODEFLY_TESTCLI_FAIL"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "testcli:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	mod, svc, err := resources.LoadModuleAndServiceUpFrom(ctx, dir)
	if err != nil {
		return err
	}
	if mod == nil || svc == nil {
		return fmt.Errorf("no Codefly service in %s", dir)
	}
	if len(svc.ServiceDependencies) != 1 {
		return fmt.Errorf("service %s must declare exactly one dependency", svc.Name)
	}
	dependency := svc.ServiceDependencies[0]

	endpoint, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	identity := fmt.Sprintf("%s/%s", mod.Name, svc.Name)
	go greet(endpoint, identity)

	control, owner, err := listenControl()
	if err != nil {
		return err
	}
	server := grpc.NewServer()
	v0.RegisterCLIServer(server, &cli{
		identity:   identity,
		dependency: dependency,
		address:    endpoint.Addr().String(),
		fail:       os.Getenv(failEnvironment),
		owner:      owner,
	})
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	return server.Serve(control)
}

// listenControl binds the control channel the SDK selected. An isolated
// session hands over a private Unix socket and the identity the server must
// prove it holds; a shared channel hands over a TCP port and no identity.
func listenControl() (net.Listener, *session.Session, error) {
	if socket := os.Getenv(session.SocketEnvironment); socket != "" {
		owner := session.FromEnvironment(os.Environ())
		if owner == nil {
			return nil, nil, fmt.Errorf("%s was set without a session identity", session.SocketEnvironment)
		}
		listener, err := net.Listen("unix", socket)
		if err != nil {
			return nil, nil, err
		}
		return listener, owner, nil
	}
	port := os.Getenv("CODEFLY_CLI_SERVER_PORT")
	if port == "" {
		return nil, nil, fmt.Errorf("no control channel was provided")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return nil, nil, err
	}
	return listener, nil, nil
}

// greet answers every connection with the identity of the session that owns
// this dependency, which is how a test proves it reached its own endpoint.
func greet(listener net.Listener, identity string) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_, _ = fmt.Fprintln(conn, identity)
		_ = conn.Close()
	}
}

type cli struct {
	v0.UnimplementedCLIServer
	identity   string
	dependency *resources.ServiceDependency
	address    string
	fail       string
	owner      *session.Session
}

// SessionHandshake proves this server is the child the SDK started, which the
// SDK requires before it drives an isolated session.
func (c *cli) SessionHandshake(_ context.Context, req *v0.SessionHandshakeRequest) (*v0.SessionHandshakeResponse, error) {
	if c.owner == nil {
		return nil, fmt.Errorf("this control channel carries no session identity")
	}
	return &v0.SessionHandshakeResponse{
		SessionId:       c.owner.ID,
		Proof:           c.owner.Proof(req.GetChallenge()),
		ProtocolVersion: session.ProtocolVersion,
		Capabilities:    []string{session.IsolatedControlSocketCapability},
	}, nil
}

func (c *cli) failed(rpc string) error {
	if c.fail != rpc {
		return nil
	}
	return fmt.Errorf("%s is unavailable", rpc)
}

func (c *cli) Ping(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (c *cli) GetFlowStatus(context.Context, *emptypb.Empty) (*v0.FlowStatus, error) {
	return &v0.FlowStatus{Ready: true}, nil
}

func (c *cli) GetDependenciesNetworkMappings(_ context.Context, _ *v0.GetNetworkMappingsRequest) (*v0.GetNetworkMappingsResponse, error) {
	if err := c.failed("GetDependenciesNetworkMappings"); err != nil {
		return nil, err
	}
	return &v0.GetNetworkMappingsResponse{
		NetworkMappings: []*basev0.NetworkMapping{
			{
				Endpoint: &basev0.Endpoint{
					Module:     c.dependency.Module,
					Service:    c.dependency.Name,
					Name:       "tcp",
					Api:        "tcp",
					Visibility: resources.VisibilityPrivate,
				},
				Instances: []*basev0.NetworkInstance{
					{Access: resources.NewNativeNetworkAccess(), Address: c.address},
				},
			},
		},
	}, nil
}

func (c *cli) GetConfiguration(_ context.Context, _ *v0.GetConfigurationRequest) (*v0.GetConfigurationResponse, error) {
	if err := c.failed("GetConfiguration"); err != nil {
		return nil, err
	}
	return &v0.GetConfigurationResponse{
		Configuration: &basev0.Configuration{
			Origin: resources.ConfigurationWorkspace,
			Infos: []*basev0.ConfigurationInformation{
				{
					Name: "session",
					ConfigurationValues: []*basev0.ConfigurationValue{
						{Key: "identity", Value: c.identity},
						{Key: "token", Value: c.identity + "-token", Secret: true},
					},
				},
			},
		},
	}, nil
}

func (c *cli) GetDependenciesConfigurations(_ context.Context, _ *v0.GetConfigurationRequest) (*v0.GetConfigurationsResponse, error) {
	if err := c.failed("GetDependenciesConfigurations"); err != nil {
		return nil, err
	}
	return &v0.GetConfigurationsResponse{
		Configurations: []*basev0.Configuration{
			{
				Origin:         c.dependency.Unique(),
				RuntimeContext: resources.NewRuntimeContextNative(),
				Infos: []*basev0.ConfigurationInformation{
					{
						Name: "connection",
						ConfigurationValues: []*basev0.ConfigurationValue{
							{Key: "connection", Value: c.address},
						},
					},
				},
			},
		},
	}, nil
}

func (c *cli) StopFlow(context.Context, *v0.StopFlowRequest) (*v0.StopFlowResponse, error) {
	return &v0.StopFlowResponse{}, nil
}

func (c *cli) DestroyFlow(context.Context, *v0.DestroyFlowRequest) (*v0.DestroyFlowResponse, error) {
	return &v0.DestroyFlowResponse{}, nil
}
