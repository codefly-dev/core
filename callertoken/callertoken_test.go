package callertoken_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/codefly-dev/core/callertoken"
)

const token = "89d0d1f6d0e2f5a4c3b2a1908f7e6d5c"

// echoDesc is a small real service with one unary and one streaming method, so
// the interceptors are driven over a real gRPC server on both paths. The
// health service alone cannot do it: its streaming method is exempt.
var echoDesc = grpc.ServiceDesc{
	ServiceName: "callertoken.test.Echo",
	HandlerType: (*any)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "Ping",
		Handler: func(_ any, ctx context.Context, dec func(any) error, ic grpc.UnaryServerInterceptor) (any, error) {
			in := new(emptypb.Empty)
			if err := dec(in); err != nil {
				return nil, err
			}
			call := func(context.Context, any) (any, error) { return &emptypb.Empty{}, nil }
			if ic == nil {
				return call(ctx, in)
			}
			return ic(ctx, in, &grpc.UnaryServerInfo{FullMethod: "/callertoken.test.Echo/Ping"}, call)
		},
	}},
	Streams: []grpc.StreamDesc{{
		StreamName:    "Chat",
		ServerStreams: true,
		ClientStreams: true,
		Handler: func(_ any, ss grpc.ServerStream) error {
			in := new(emptypb.Empty)
			if err := ss.RecvMsg(in); err != nil {
				return err
			}
			return ss.SendMsg(&emptypb.Empty{})
		},
	}},
}

// serve starts a real server guarded by the interceptors for serverToken and
// returns a connection to it dialled with opts.
func serve(t *testing.T, serverToken string, opts ...grpc.DialOption) *grpc.ClientConn {
	t.Helper()
	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(callertoken.UnaryServerInterceptor(serverToken)),
		grpc.ChainStreamInterceptor(callertoken.StreamServerInterceptor(serverToken)),
	)
	srv.RegisterService(&echoDesc, struct{}{})
	healthpb.RegisterHealthServer(srv, health.NewServer())

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func ping(c context.Context, conn *grpc.ClientConn) error {
	return conn.Invoke(c, "/callertoken.test.Echo/Ping", &emptypb.Empty{}, &emptypb.Empty{})
}

// chat opens the stream, sends one message and waits for the reply, so a
// refusal at stream open and a refusal after the first frame both surface.
func chat(c context.Context, conn *grpc.ClientConn) error {
	st, err := conn.NewStream(c, &grpc.StreamDesc{ClientStreams: true, ServerStreams: true}, "/callertoken.test.Echo/Chat")
	if err != nil {
		return err
	}
	if err := st.SendMsg(&emptypb.Empty{}); err != nil {
		return err
	}
	return st.RecvMsg(&emptypb.Empty{})
}

func requireCode(t *testing.T, err error, want codes.Code, contains string) {
	t.Helper()
	if status.Code(err) != want {
		t.Fatalf("code = %v (%v), want %v", status.Code(err), err, want)
	}
	if contains != "" && !strings.Contains(err.Error(), contains) {
		t.Fatalf("error %q does not contain %q", err, contains)
	}
}

func TestPresentedTokenIsAdmittedOnUnaryAndStream(t *testing.T) {
	conn := serve(t, token, callertoken.DialOption(token))
	if err := ping(ctx(t), conn); err != nil {
		t.Fatalf("unary with the token: %v", err)
	}
	if err := chat(ctx(t), conn); err != nil {
		t.Fatalf("stream with the token: %v", err)
	}
}

func TestEveryWayOfNotPresentingTheTokenIsRefused(t *testing.T) {
	cases := []struct {
		name     string
		md       metadata.MD
		contains string
	}{
		{"no metadata", nil, "missing " + callertoken.MetadataKey},
		{"other metadata only", metadata.Pairs("x-other", "irrelevant"), "missing " + callertoken.MetadataKey},
		{"empty token", metadata.Pairs(callertoken.MetadataKey, ""), "missing " + callertoken.MetadataKey},
		{"wrong token", metadata.Pairs(callertoken.MetadataKey, "wrong"), "invalid " + callertoken.MetadataKey},
		{"token with a suffix", metadata.Pairs(callertoken.MetadataKey, token+"0"), "invalid " + callertoken.MetadataKey},
	}
	conn := serve(t, token)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ctx(t)
			if tc.md != nil {
				c = metadata.NewOutgoingContext(c, tc.md)
			}
			requireCode(t, ping(c, conn), codes.Unauthenticated, tc.contains)
			requireCode(t, chat(c, conn), codes.Unauthenticated, tc.contains)
		})
	}
}

func TestRefusalNeverEchoesTheExpectedToken(t *testing.T) {
	conn := serve(t, token)
	err := ping(metadata.AppendToOutgoingContext(ctx(t), callertoken.MetadataKey, "wrong"), conn)
	if strings.Contains(err.Error(), token) {
		t.Fatalf("refusal leaked the expected token: %v", err)
	}
}

// A server with no token configured is a misconfiguration; even a caller that
// presents an empty token must not match it.
func TestServerWithoutATokenFailsClosed(t *testing.T) {
	conn := serve(t, "")
	c := ctx(t)
	requireCode(t, ping(c, conn), codes.Unauthenticated, "")
	c = metadata.AppendToOutgoingContext(c, callertoken.MetadataKey, "")
	requireCode(t, ping(c, conn), codes.Unauthenticated, "")
	requireCode(t, chat(c, conn), codes.Unauthenticated, "")
}

// The readiness probe presents no token. Check and Watch are admitted; List,
// which enumerates every registered service, is not.
func TestHealthCheckAndWatchAreAdmittedWithoutAToken(t *testing.T) {
	conn := serve(t, token)
	client := healthpb.NewHealthClient(conn)

	resp, err := client.Check(ctx(t), &healthpb.HealthCheckRequest{})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("Check without a token = %v, %v", resp, err)
	}
	w, err := client.Watch(ctx(t), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := w.Recv(); err != nil || got.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("Watch without a token = %v, %v", got, err)
	}
	_, err = client.List(ctx(t), &healthpb.HealthListRequest{})
	requireCode(t, err, codes.Unauthenticated, "")
}

func TestIsHealthMethodIsExact(t *testing.T) {
	for method, want := range map[string]bool{
		healthpb.Health_Check_FullMethodName: true,
		healthpb.Health_Watch_FullMethodName: true,
		healthpb.Health_List_FullMethodName:  false,
		"/grpc.health.v1.Health/":            false,
		"/grpc.health.v1.HealthExtra/Check":  false,
		"/callertoken.test.Echo/Ping":        false,
	} {
		if got := callertoken.IsHealthMethod(method); got != want {
			t.Errorf("IsHealthMethod(%q) = %v, want %v", method, got, want)
		}
	}
}

func TestPerRPCCredentialsCarryTheTokenWithoutRequiringTLS(t *testing.T) {
	creds := callertoken.PerRPCCredentials(token)
	md, err := creds.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(md) != 1 || md[callertoken.MetadataKey] != token {
		t.Fatalf("metadata = %v", md)
	}
	if creds.RequireTransportSecurity() {
		t.Fatal("the credential must be usable over unix sockets, loopback and a mesh")
	}
}

func TestMetadataKeyIsTheWireKeyTheFleetUses(t *testing.T) {
	if callertoken.MetadataKey != "x-codefly-token" {
		t.Fatalf("MetadataKey = %q: changing it breaks every deployed agent and gateway", callertoken.MetadataKey)
	}
}

func TestGenerateIsHexOf256BitsAndFresh(t *testing.T) {
	seen := map[string]bool{}
	for range 16 {
		tok, err := callertoken.Generate()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != 64 || strings.Trim(tok, "0123456789abcdef") != "" {
			t.Fatalf("token %q is not 64 lowercase hex characters", tok)
		}
		if seen[tok] {
			t.Fatalf("token %q generated twice", tok)
		}
		seen[tok] = true
	}
}
