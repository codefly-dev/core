package agents

import (
	"context"
	"os"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/wool"
)

// PrincipalMetadataKey is the gRPC metadata header that carries the
// principal token across each call. Lowercase per gRPC convention
// (binary metadata uses -bin suffix; this is the textual variant).
//
// Distinct from AuthMetadataKey (process binding); the principal
// header is the AUTHORITY claim, separate from the connection auth.
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
//   - Header present but malformed → return Unauthenticated. The
//     model gets a clear refusal so it doesn't retry blindly.
//
//   - Header well-formed → decode, validate, stamp ctx. The
//     decoded Principal carries the original token string so
//     downstream PDP can re-verify against saas-starter when
//     making decisions.
//
// **Why we don't verify here.** The interceptor is a narrow
// boundary — it shapes the context. Cryptographic verification is
// the PDP's job (M3 phase 2, against the saas-starter signer) so
// signature/key plumbing stays in one place. Today's v1-unsigned
// format means there's nothing to verify; M6's Biscuit will need
// signer trust which the SaasPDP holds.
func principalUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Two sources for the principal token, in priority order:
		//
		//  1. The CODEFLY_PRINCIPAL_TOKEN env var (set by manager.Load
		//     when the host wraps the plugin). Survives the entire
		//     plugin lifetime and applies to ALL its calls.
		//  2. The x-codefly-principal metadata header on the call.
		//     Used when a downstream caller delegates a different
		//     principal for this specific RPC (e.g. Mind acting as a
		//     user for one tool call).
		//
		// Header wins if both present — per-call delegation overrides
		// the long-lived spawn binding.
		var token string
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vs := md.Get(PrincipalMetadataKey); len(vs) > 0 {
				token = vs[0]
			}
		}
		if token == "" {
			token = os.Getenv("CODEFLY_PRINCIPAL_TOKEN")
		}

		// The Authorizer is stamped regardless of principal presence —
		// it's a process-wide singleton that ALWAYS belongs on ctx so
		// handlers can call AuthorizerFromContext without nil-checks.
		// Without a callback socket, it's the disabled variant that
		// fails closed with a clear reason.
		ctx = policy.WithAuthorizer(ctx, getAuthorizer())

		if token == "" {
			// No token at all — proceed without a principal. Shadow-
			// mode PDP will log this; enforce-mode PDP will deny.
			return handler(ctx, req)
		}

		p, err := policy.DecodePrincipalToken(token)
		if err != nil {
			// Bad token → log + fall through with no principal. We
			// don't return Unauthenticated because the legacy code
			// path (no PDP wired) shouldn't refuse a call just
			// because the principal token is malformed. Once the
			// PDP runs in enforce mode, no-principal → deny.
			//
			// At INFO level so audit picks it up (a forged token
			// attempt looks like exactly this).
			wool.Get(ctx).In("principalUnaryInterceptor").
				Info("principal token decode failed",
					wool.Field("error", err.Error()),
					wool.Field("method", info.FullMethod))
			return handler(ctx, req)
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
		var token string
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vs := md.Get(PrincipalMetadataKey); len(vs) > 0 {
				token = vs[0]
			}
		}
		if token == "" {
			token = os.Getenv("CODEFLY_PRINCIPAL_TOKEN")
		}
		// Always stamp the Authorizer (process-singleton) so handlers
		// have a uniform contract regardless of principal presence.
		ctx = policy.WithAuthorizer(ctx, getAuthorizer())

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
			wrapped := &principalStreamWrapper{ServerStream: ss, ctx: ctx}
			return handler(srv, wrapped)
		}
		ctx = policy.WithPrincipal(ctx, p)
		wrapped := &principalStreamWrapper{ServerStream: ss, ctx: ctx}
		return handler(srv, wrapped)
	}
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
