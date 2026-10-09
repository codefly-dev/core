package agents

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The caller-token interceptors live in, and are tested through,
// callertoken; AuthMetadataKey must stay the same wire key.
func TestAuthMetadataKeyIsTheCallerTokenKey(t *testing.T) {
	if AuthMetadataKey != "x-codefly-token" {
		t.Fatalf("AuthMetadataKey = %q", AuthMetadataKey)
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
