package agents

import (
	"context"
	"fmt"
	"os"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/wool"
)

// PrincipalMetadataKey is reserved. Principal authority is spawn-bound; a
// caller attempting to replace it per RPC is rejected because the permission
// callback and scoped secret are also bound to that spawn.
const PrincipalMetadataKey = "x-codefly-principal"

// principalUnaryInterceptor is a gRPC server-side interceptor that
// extracts the principal claim from incoming metadata and stamps it
// on the context for downstream handlers (the policyguard.Guard +
// PDP). Designed to chain AFTER authUnaryInterceptor — auth proves
// the connection; this interceptor reads the authority on top.
//
// Behavior:
//
//   - No metadata header → fall through with no principal stamped.
//     Downstream PDP treats this as "anonymous" and applies its
//     own policy (typically deny in enforce mode, log in shadow).
//
//   - Per-call principal header present → return Unauthenticated. A session is
//     one principal; hosts needing another principal open another session.
//
//   - Header well-formed → decode, validate, stamp ctx. The
//     decoded Principal carries the original token string so
//     downstream PDP can re-verify against saas-starter when
//     making decisions.
//
// Credential signature verification remains the host/PDP's responsibility;
// this interceptor enforces that the already-resolved claim cannot be swapped.
func principalUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// The Authorizer is stamped regardless of principal presence —
		// it's a process-wide singleton that ALWAYS belongs on ctx so
		// handlers can call AuthorizerFromContext without nil-checks.
		// Without a callback socket, it's the disabled variant that
		// fails closed with a clear reason.
		ctx = policy.WithAuthorizer(ctx, getAuthorizer())
		token, err := spawnBoundPrincipalToken(ctx)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, err.Error())
		}

		if token == "" {
			// No token at all — proceed without a principal. Shadow-
			// mode PDP will log this; enforce-mode PDP will deny.
			return handler(ctx, req)
		}

		p, err := policy.DecodePrincipalToken(token)
		if err != nil {
			wool.Get(ctx).In("principalUnaryInterceptor").
				Info("principal token decode failed",
					wool.Field("error", err.Error()),
					wool.Field("method", info.FullMethod))
			return nil, status.Error(codes.Unauthenticated, "spawn-bound principal token is invalid")
		}

		ctx = policy.WithPrincipal(ctx, p)
		return handler(ctx, req)
	}
}

// principalStreamInterceptor mirrors principalUnaryInterceptor for
// streaming RPCs. The same source-priority rule applies.
func principalStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		// Always stamp the Authorizer (process-singleton) so handlers
		// have a uniform contract regardless of principal presence.
		ctx = policy.WithAuthorizer(ctx, getAuthorizer())
		token, tokenErr := spawnBoundPrincipalToken(ctx)
		if tokenErr != nil {
			return status.Error(codes.Unauthenticated, tokenErr.Error())
		}

		if token == "" {
			wrapped := &principalStreamWrapper{ServerStream: ss, ctx: ctx}
			return handler(srv, wrapped)
		}
		p, err := policy.DecodePrincipalToken(token)
		if err != nil {
			wool.Get(ctx).In("principalStreamInterceptor").
				Info("principal token decode failed",
					wool.Field("error", err.Error()),
					wool.Field("method", info.FullMethod))
			return status.Error(codes.Unauthenticated, "spawn-bound principal token is invalid")
		}
		ctx = policy.WithPrincipal(ctx, p)
		wrapped := &principalStreamWrapper{ServerStream: ss, ctx: ctx}
		return handler(srv, wrapped)
	}
}

func spawnBoundPrincipalToken(ctx context.Context) (string, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok && len(md.Get(PrincipalMetadataKey)) > 0 {
		return "", fmt.Errorf("per-call principal override is forbidden; open a separate ToolboxSession")
	}
	return os.Getenv("CODEFLY_PRINCIPAL_TOKEN"), nil
}

// getAuthorizer returns the process-singleton Authorizer (lazy-
// constructed from env on first call). Plugin handlers stamp this
// on every incoming request via the interceptor.
//
// Process-singleton because the Authorizer holds an http.Client
// with connection-reuse — recreating it per request would defeat
// keep-alive on the UDS connection to the host's permission
// callback server.
func getAuthorizer() policy.Authorizer {
	authorizerOnce.Do(func() {
		processAuthorizer = policy.NewCallbackAuthorizerFromEnv()
	})
	return processAuthorizer
}

var (
	authorizerOnce    sync.Once
	processAuthorizer policy.Authorizer
)

// principalStreamWrapper overrides ServerStream.Context() to return
// the principal-stamped context. Other ServerStream methods pass
// through unchanged.
type principalStreamWrapper struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *principalStreamWrapper) Context() context.Context {
	return w.ctx
}
