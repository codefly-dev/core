// Package callertoken is the one implementation of codefly's caller token: a
// shared secret a client presents as the "x-codefly-token" gRPC metadata header
// and a server compares in constant time before it runs a handler.
//
// The codefly host uses it to talk to agent plugins (a fresh token per spawn,
// see agents.Serve and the agent manager), and a service that publishes its own
// gRPC listener — an object-storage or warehouse gateway, say — uses the same
// header to admit only the callers that were handed its token. The package
// depends on grpc alone, so a gateway image can import it without linking the
// agent runtime.
//
// It authenticates and nothing else: whether the caller holds the token, never
// what that caller may do with it.
package callertoken

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// MetadataKey is the gRPC metadata key carrying the token. Lowercase per gRPC
// convention: metadata keys are case-insensitive but the wire form is lowercase.
const MetadataKey = "x-codefly-token"

// Health-service methods a readiness probe calls. They return SERVING or
// NOT_SERVING and nothing privileged, and a probe — the codefly host's, a load
// balancer's, a mesh's — presents no token, so guarding them would make the
// server look permanently unready to everything that would route to it.
//
// The set is enumerated rather than matched by prefix: the health service's
// List method returns every registered service name, and a prefix would admit
// it, and any method added to the service later, without a token.
const (
	healthCheck = "/grpc.health.v1.Health/Check"
	healthWatch = "/grpc.health.v1.Health/Watch"
)

// IsHealthMethod reports whether fullMethod is a health-check method the
// interceptors admit without a token.
func IsHealthMethod(fullMethod string) bool {
	return fullMethod == healthCheck || fullMethod == healthWatch
}

// tokenBytes is the entropy of a generated token: 256 bits, far beyond what a
// local-attacker threat model needs, and cheap.
const tokenBytes = 32

// Generate returns a new random token: 32 bytes from crypto/rand, hex-encoded
// to 64 characters. Hex rather than base64 so the token survives env-var
// quoting through every shell and exec layer between a host and the process it
// hands the token to.
func Generate() (string, error) {
	var buf [tokenBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// UnaryServerInterceptor refuses every unary call that does not present token,
// except the health check. An empty token is a server misconfiguration and
// fails closed: no caller can match it, so none is admitted. A server that
// accepts anonymous callers installs no interceptor instead of one built on "".
func UnaryServerInterceptor(token string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !IsHealthMethod(info.FullMethod) {
			if err := verify(ctx, token); err != nil {
				return nil, err
			}
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor is UnaryServerInterceptor for streaming calls. gRPC
// runs it before the handler observes a single frame, so an unauthorized stream
// is refused at open rather than after its first message is accepted.
func StreamServerInterceptor(token string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !IsHealthMethod(info.FullMethod) {
			if err := verify(ss.Context(), token); err != nil {
				return err
			}
		}
		return handler(srv, ss)
	}
}

// verify reads the token from the incoming metadata and compares it in
// constant time. Every failure is codes.Unauthenticated and names the header,
// never the expected value.
func verify(ctx context.Context, expected string) error {
	if expected == "" {
		return status.Error(codes.Unauthenticated, "no "+MetadataKey+" is configured on this server")
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing "+MetadataKey)
	}
	presented := md.Get(MetadataKey)
	// An empty presented value is refused as absent rather than reaching the
	// comparison.
	if len(presented) == 0 || presented[0] == "" {
		return status.Error(codes.Unauthenticated, "missing "+MetadataKey)
	}
	// Comparing as byte slices in constant time avoids the timing side channel
	// of ==.
	if subtle.ConstantTimeCompare([]byte(presented[0]), []byte(expected)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid "+MetadataKey)
	}
	return nil
}

// PerRPCCredentials returns the client side: credentials that attach token to
// every call a connection makes.
//
// RequireTransportSecurity is false. The token authenticates the caller; the
// transport is the deployment's decision, and the profiles that use this reach
// the server over a unix socket, loopback, the Docker host bridge, or a mesh
// that terminates transport security itself. Demanding TLS here would make the
// credential unusable on all of them.
func PerRPCCredentials(token string) credentials.PerRPCCredentials {
	return bearer(token)
}

// DialOption is PerRPCCredentials as a grpc.DialOption.
func DialOption(token string) grpc.DialOption {
	return grpc.WithPerRPCCredentials(bearer(token))
}

type bearer string

func (b bearer) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{MetadataKey: string(b)}, nil
}

func (b bearer) RequireTransportSecurity() bool { return false }
