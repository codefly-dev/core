package manager

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestHealthResponseReadyOnlyAllowsServingOrLegacyUnimplemented(t *testing.T) {
	ctx := context.Background()
	if !healthResponseReady(ctx, &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil) {
		t.Fatal("SERVING response was rejected")
	}
	if !healthResponseReady(ctx, nil, status.Error(codes.Unimplemented, "legacy agent")) {
		t.Fatal("legacy unimplemented health endpoint was rejected")
	}
	for name, err := range map[string]error{
		"unavailable": status.Error(codes.Unavailable, "connection lost"),
		"deadline":    status.Error(codes.DeadlineExceeded, "timeout"),
		"unknown":     status.Error(codes.Unknown, "server failed"),
	} {
		t.Run(name, func(t *testing.T) {
			if healthResponseReady(ctx, nil, err) {
				t.Fatalf("%v was promoted to ready", err)
			}
		})
	}
	if healthResponseReady(ctx, &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_NOT_SERVING}, nil) {
		t.Fatal("NOT_SERVING response was accepted")
	}
}
