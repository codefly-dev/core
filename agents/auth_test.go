package agents

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TestAuthUnaryInterceptor_RejectsMissingToken pins the load-bearing
// behavior: when the plugin is configured with an expected token,
// any call without a matching bearer in metadata is rejected with
// Unauthenticated before the handler runs.
func TestAuthUnaryInterceptor_RejectsMissingToken(t *testing.T) {
	const token = "deadbeef-correct-token"

	intercept := authUnaryInterceptor(token)
	handlerRan := false
	handler := func(_ context.Context, _ any) (any, error) {
		handlerRan = true
		return nil, nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/example.Service/Method"}

	t.Run("no metadata at all", func(t *testing.T) {
		handlerRan = false
		_, err := intercept(context.Background(), nil, info, handler)
		st, _ := status.FromError(err)
		if st.Code() != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v: %v", st.Code(), err)
		}
		if !strings.Contains(st.Message(), "no metadata") {
			t.Fatalf("error must point at no-metadata cause; got %q", st.Message())
		}
		if handlerRan {
			t.Fatal("handler MUST NOT run when auth fails")
		}
	})

	t.Run("metadata without bearer", func(t *testing.T) {
		handlerRan = false
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs("x-other-key", "irrelevant"))
		_, err := intercept(ctx, nil, info, handler)
		st, _ := status.FromError(err)
		if st.Code() != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", st.Code())
		}
		if !strings.Contains(st.Message(), "missing auth token") {
			t.Fatalf("error must say bearer is missing; got %q", st.Message())
		}
		if handlerRan {
			t.Fatal("handler MUST NOT run when auth fails")
		}
	})

	t.Run("wrong bearer", func(t *testing.T) {
		handlerRan = false
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs(AuthMetadataKey, "wrong-token"))
		_, err := intercept(ctx, nil, info, handler)
		st, _ := status.FromError(err)
		if st.Code() != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated, got %v", st.Code())
		}
		if !strings.Contains(st.Message(), "bad auth token") {
			t.Fatalf("error must say token mismatch; got %q", st.Message())
		}
		if handlerRan {
			t.Fatal("handler MUST NOT run when auth fails")
		}
	})

	t.Run("correct bearer accepts and runs handler", func(t *testing.T) {
		handlerRan = false
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs(AuthMetadataKey, token))
		_, err := intercept(ctx, nil, info, handler)
		if err != nil {
			t.Fatalf("matching token must accept: %v", err)
		}
		if !handlerRan {
			t.Fatal("handler MUST run when auth passes")
		}
	})
}

// TestAuthUnaryInterceptor_HealthExempt confirms health-check methods
// pass through without bearer. Some gRPC bootstraps fire Check before
// the host has wired client-side metadata; if we required auth here,
// the readiness probe would race-fail.
func TestAuthUnaryInterceptor_HealthExempt(t *testing.T) {
	intercept := authUnaryInterceptor("a-required-token")
	handlerRan := false
	handler := func(_ context.Context, _ any) (any, error) {
		handlerRan = true
		return nil, nil
	}

	for _, method := range []string{
		"/grpc.health.v1.Health/Check",
		"/grpc.health.v1.Health/Watch",
	} {
		t.Run(method, func(t *testing.T) {
			handlerRan = false
			info := &grpc.UnaryServerInfo{FullMethod: method}
			// No bearer in context — would fail for any other method.
			if _, err := intercept(context.Background(), nil, info, handler); err != nil {
				t.Fatalf("health-exempt method must pass without bearer: %v", err)
			}
			if !handlerRan {
				t.Fatal("handler MUST run for health methods even without auth")
			}
		})
	}
}

func TestAuthUnaryInterceptor_NoExpectedTokenFailsClosed(t *testing.T) {
	intercept := authUnaryInterceptor("")
	handlerRan := false
	handler := func(_ context.Context, _ any) (any, error) {
		handlerRan = true
		return nil, nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/example.Service/Method"}

	_, err := intercept(context.Background(), nil, info, handler)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing server token must fail closed with Unauthenticated: %v", err)
	}
	if handlerRan {
		t.Fatal("handler MUST NOT run when the server has no expected token")
	}
}

func TestPanicRecoveryInterceptorRedactsPanicValue(t *testing.T) {
	const secret = "database-password-must-not-escape"
	intercept := panicRecoveryInterceptor()
	_, err := intercept(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/example.Service/Call"},
		func(context.Context, any) (any, error) { panic(secret) })
	if status.Code(err) != codes.Internal {
		t.Fatalf("panic must normalize to Internal: %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("panic value leaked through RPC error: %v", err)
	}
}

// TestVerifyAuthToken_ConstantTime confirms a passing token actually
// passes (regression for "someone replaced the constant-time compare
// with ==").
func TestVerifyAuthToken_ConstantTime(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(AuthMetadataKey, "expected"))
	if err := verifyAuthToken(ctx, "expected"); err != nil {
		t.Fatalf("matching token should pass: %v", err)
	}
}
