package solution_test

import (
	"context"
	"net"
	"testing"

	solutionv0 "github.com/codefly-dev/core/generated/go/codefly/services/solution/v0"
	"github.com/codefly-dev/core/solution"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestSolutionRPCMethodPolicyIsMachineEnforceable(t *testing.T) {
	expected := map[string]*solutionv0.SolutionMethodPolicy{
		"GetSolutionInformation": {Network: solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_OFFLINE, Effect: solutionv0.SolutionEffect_SOLUTION_EFFECT_READ_ONLY},
		"Create":                 {Network: solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_OFFLINE, Effect: solutionv0.SolutionEffect_SOLUTION_EFFECT_LOCAL_WRITE},
		"Update":                 {Network: solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_OFFLINE, Effect: solutionv0.SolutionEffect_SOLUTION_EFFECT_LOCAL_WRITE},
		"Package":                {Network: solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_REGISTRY_WRITE, Effect: solutionv0.SolutionEffect_SOLUTION_EFFECT_REGISTRY_WRITE},
		"Render":                 {Network: solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_OFFLINE, Effect: solutionv0.SolutionEffect_SOLUTION_EFFECT_LOCAL_WRITE},
	}

	service := solutionv0.File_codefly_services_solution_v0_solution_proto.Services().ByName("Solution")
	require.NotNil(t, service)
	require.Equal(t, len(expected), service.Methods().Len())
	for i := 0; i < service.Methods().Len(); i++ {
		method := service.Methods().Get(i)
		options := method.Options().(*descriptorpb.MethodOptions)
		require.True(t, proto.HasExtension(options, solutionv0.E_SolutionMethodPolicy), method.Name())
		policy := proto.GetExtension(options, solutionv0.E_SolutionMethodPolicy).(*solutionv0.SolutionMethodPolicy)
		want := expected[string(method.Name())]
		require.NotNil(t, want, method.Name())
		require.True(t, proto.Equal(want, policy), method.Name())
	}
}

// TestSolutionContractIsUnaryOnly guards the invariant EnforcingClientInterceptor
// relies on: the gate is unary-only, so a streaming Solution RPC would dispatch
// unchecked. Adding one must fail here until a stream gate exists.
func TestSolutionContractIsUnaryOnly(t *testing.T) {
	service := solutionv0.File_codefly_services_solution_v0_solution_proto.Services().ByName("Solution")
	require.NotNil(t, service)
	for i := 0; i < service.Methods().Len(); i++ {
		method := service.Methods().Get(i)
		require.False(t, method.IsStreamingClient(), "%s is client-streaming", method.Name())
		require.False(t, method.IsStreamingServer(), "%s is server-streaming", method.Name())
	}
}

// TestSolutionMethodPolicyAxesAreCoherent guards the two policy axes against
// incoherent combinations. Network and effect are independent in the model (as
// in provider_method_policy), but for real methods a registry push is a remote
// write and vice versa: you cannot push to a registry while offline, and no
// non-registry effect needs registry network. This catches an annotation like
// {network: OFFLINE, effect: REGISTRY_WRITE} that the ordered ceiling checks
// would otherwise silently accept.
func TestSolutionMethodPolicyAxesAreCoherent(t *testing.T) {
	service := solutionv0.File_codefly_services_solution_v0_solution_proto.Services().ByName("Solution")
	require.NotNil(t, service)
	for i := 0; i < service.Methods().Len(); i++ {
		method := service.Methods().Get(i)
		options := method.Options().(*descriptorpb.MethodOptions)
		policy := proto.GetExtension(options, solutionv0.E_SolutionMethodPolicy).(*solutionv0.SolutionMethodPolicy)
		registryNetwork := policy.GetNetwork() == solutionv0.SolutionNetworkMode_SOLUTION_NETWORK_MODE_REGISTRY_WRITE
		registryEffect := policy.GetEffect() == solutionv0.SolutionEffect_SOLUTION_EFFECT_REGISTRY_WRITE
		require.Equal(t, registryEffect, registryNetwork,
			"%s: registry network and registry effect must agree", method.Name())
	}
}

// recordingSolutionServer is a real Solution server that records which RPCs it
// handled so a test can assert a denied call never reached it.
type recordingSolutionServer struct {
	solutionv0.UnimplementedSolutionServer
	handled map[string]int
}

func (s *recordingSolutionServer) GetSolutionInformation(context.Context, *solutionv0.GetSolutionInformationRequest) (*solutionv0.GetSolutionInformationResponse, error) {
	s.handled["GetSolutionInformation"]++
	return &solutionv0.GetSolutionInformationResponse{}, nil
}

func (s *recordingSolutionServer) Create(context.Context, *solutionv0.CreateRequest) (*solutionv0.CreateResponse, error) {
	s.handled["Create"]++
	return &solutionv0.CreateResponse{}, nil
}

func (s *recordingSolutionServer) Package(context.Context, *solutionv0.PackageRequest) (*solutionv0.PackageResponse, error) {
	s.handled["Package"]++
	return &solutionv0.PackageResponse{}, nil
}

func TestEnforcingClientInterceptorDeniesOverCeilingBeforeTheWire(t *testing.T) {
	server := &recordingSolutionServer{handled: map[string]int{}}
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	solutionv0.RegisterSolutionServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(solution.EnforcingClientInterceptor()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := solutionv0.NewSolutionClient(conn)

	ctx := solution.WithCeiling(context.Background(), solution.CeilingScaffold())

	// Create is at the ceiling — admitted and reaches the server.
	_, err = client.Create(ctx, &solutionv0.CreateRequest{})
	require.NoError(t, err)
	require.Equal(t, 1, server.handled["Create"])

	// Package exceeds the ceiling — denied before crossing the wire.
	_, err = client.Package(ctx, &solutionv0.PackageRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Equal(t, 0, server.handled["Package"])

	// No ceiling on the context defaults to least privilege: the read-only
	// advertisement call is admitted and reaches the server...
	_, err = client.GetSolutionInformation(context.Background(), &solutionv0.GetSolutionInformationRequest{})
	require.NoError(t, err)
	require.Equal(t, 1, server.handled["GetSolutionInformation"])

	// ...but a mutating RPC without a ceiling is still denied before the wire,
	// and the denial names the remedy so it is not mistaken for an auth failure:
	// the missing ceiling, not the token, is what the caller must fix.
	_, err = client.Create(context.Background(), &solutionv0.CreateRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Equal(t, 1, server.handled["Create"])
	require.Contains(t, status.Convert(err).Message(), "solution.WithCeiling")
}

// TestClientRequiresCeilingPerCall proves the typed Client makes the ceiling a
// mandatory, unforgeable argument: the same low ceiling admits Create and denies
// Package over the wire, so a caller cannot dispatch a Solution RPC without
// declaring the operation it performs.
func TestClientRequiresCeilingPerCall(t *testing.T) {
	server := &recordingSolutionServer{handled: map[string]int{}}
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	solutionv0.RegisterSolutionServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(solution.EnforcingClientInterceptor()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := solution.NewClient(conn)

	// Scaffold ceiling admits Create...
	_, err = client.Create(context.Background(), solution.CeilingScaffold(), &solutionv0.CreateRequest{})
	require.NoError(t, err)
	require.Equal(t, 1, server.handled["Create"])

	// ...and denies Package, which the wrapper cannot dispatch without a
	// publish-level ceiling.
	_, err = client.Package(context.Background(), solution.CeilingScaffold(), &solutionv0.PackageRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Equal(t, 0, server.handled["Package"])
}

func TestEnforcingClientInterceptorPassesThroughNonSolutionCalls(t *testing.T) {
	interceptor := solution.EnforcingClientInterceptor()
	invoked := false
	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		invoked = true
		return nil
	}
	// No ceiling on the context, yet a non-Solution method must still dispatch.
	err := interceptor(context.Background(), "/grpc.health.v1.Health/Check", nil, nil, nil, invoker)
	require.NoError(t, err)
	require.True(t, invoked)
}

func TestSolutionPolicyEnumsAreOrderedAsCeilings(t *testing.T) {
	networks := solutionv0.SolutionNetworkMode(0).Descriptor()
	require.Equal(t, []protoreflect.Name{
		"SOLUTION_NETWORK_MODE_UNSPECIFIED",
		"SOLUTION_NETWORK_MODE_OFFLINE",
		"SOLUTION_NETWORK_MODE_REGISTRY_WRITE",
	}, enumNames(networks.Values()))

	effects := solutionv0.SolutionEffect(0).Descriptor()
	require.Equal(t, []protoreflect.Name{
		"SOLUTION_EFFECT_UNSPECIFIED",
		"SOLUTION_EFFECT_READ_ONLY",
		"SOLUTION_EFFECT_LOCAL_WRITE",
		"SOLUTION_EFFECT_REGISTRY_WRITE",
	}, enumNames(effects.Values()))
}

func enumNames(values protoreflect.EnumValueDescriptors) []protoreflect.Name {
	out := make([]protoreflect.Name, values.Len())
	for i := 0; i < values.Len(); i++ {
		out[i] = values.Get(i).Name()
	}
	return out
}
