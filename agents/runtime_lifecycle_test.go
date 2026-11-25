package agents

import (
	"context"
	"sync/atomic"
	"testing"

	runtimev1 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v1"
	"google.golang.org/grpc"
)

func TestRuntimeLoadTrackerIgnoresBuilderOnlyRPCs(t *testing.T) {
	var loaded atomic.Bool
	interceptor := runtimeLoadTracker(&loaded)
	handler := func(context.Context, any) (any, error) { return nil, nil }
	if _, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/codefly.services.builder.v1.Builder/Load"}, handler); err != nil {
		t.Fatal(err)
	}
	if loaded.Load() {
		t.Fatal("builder RPC marked the runtime lifecycle loaded")
	}
}

func TestRuntimeLoadTrackerMarksRuntimeBeforeHandler(t *testing.T) {
	var loaded atomic.Bool
	interceptor := runtimeLoadTracker(&loaded)
	handler := func(context.Context, any) (any, error) {
		if !loaded.Load() {
			t.Fatal("runtime was not marked loaded before entering its handler")
		}
		return nil, nil
	}
	if _, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: runtimev1.Runtime_Load_FullMethodName}, handler); err != nil {
		t.Fatal(err)
	}
}
