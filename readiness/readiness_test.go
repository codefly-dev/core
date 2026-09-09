package readiness_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/readiness"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
)

// serveGRPC starts a real gRPC server and returns its address. registerHealth
// controls whether grpc.health.v1 is served at all, which is the difference
// between a generated Codefly server and a hand-customized one.
func serveGRPC(t *testing.T, registerHealth bool, serving healthpb.HealthCheckResponse_ServingStatus) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	if registerHealth {
		checker := health.NewServer()
		checker.SetServingStatus("", serving)
		checker.SetServingStatus("accounts.v1.Accounts", serving)
		healthpb.RegisterHealthServer(server, checker)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

// listenAndClose returns an address nothing listens on any more: a service that
// died after a successful start.
func listenAndClose(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	return address
}

func requirement(t *testing.T, endpoint, api string, probe *resources.Probe) *resources.ReadinessRequirement {
	t.Helper()
	wire, err := probe.Proto(api)
	require.NoError(t, err)
	return &resources.ReadinessRequirement{Dependency: "saas/accounts", Endpoint: endpoint, API: api, Probe: wire, Declared: true}
}

func TestGrpcHealthProbeDiscriminatesServingFromReachable(t *testing.T) {
	ctx := context.Background()
	probe := &resources.Probe{Kind: resources.ProbeKindGRPCHealth, Service: "accounts.v1.Accounts"}

	serving := serveGRPC(t, true, healthpb.HealthCheckResponse_SERVING)
	result := readiness.Check(ctx, requirement(t, standards.GRPC, standards.GRPC, probe), readiness.Target{Address: serving})
	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_PASSED, result.Outcome)

	notServing := serveGRPC(t, true, healthpb.HealthCheckResponse_NOT_SERVING)
	result = readiness.Check(ctx, requirement(t, standards.GRPC, standards.GRPC, probe), readiness.Target{Address: notServing})
	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_FAILED, result.Outcome)
	require.Equal(t, basev0.ProbeFailureKind_PROBE_FAILURE_KIND_NOT_SERVING, result.FailureKind)

	// A reachable server with no health service is not silently promoted to
	// ready: it is a distinct, named failure.
	noHealth := serveGRPC(t, false, healthpb.HealthCheckResponse_SERVING)
	result = readiness.Check(ctx, requirement(t, standards.GRPC, standards.GRPC, probe), readiness.Target{Address: noHealth})
	require.Equal(t, basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNIMPLEMENTED, result.FailureKind)

	result = readiness.Check(ctx, requirement(t, standards.GRPC, standards.GRPC, probe), readiness.Target{Address: listenAndClose(t)})
	require.Equal(t, basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNREACHABLE, result.FailureKind)
}

func TestHTTPProbeRejectsServiceUnavailable(t *testing.T) {
	ctx := context.Background()
	probe := &resources.Probe{Kind: resources.ProbeKindHTTP, Path: "/healthz", BodyContains: "ok"}

	statuses := map[int]basev0.ProbeOutcome{
		http.StatusOK:                 basev0.ProbeOutcome_PROBE_OUTCOME_PASSED,
		http.StatusServiceUnavailable: basev0.ProbeOutcome_PROBE_OUTCOME_FAILED,
		http.StatusNotFound:           basev0.ProbeOutcome_PROBE_OUTCOME_FAILED,
	}
	for code, want := range statuses {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte("ok"))
		}))
		t.Cleanup(server.Close)

		result := readiness.Check(ctx, requirement(t, standards.HTTP, standards.HTTP, probe),
			readiness.Target{Address: strings.TrimPrefix(server.URL, "http://")})
		require.Equal(t, want, result.Outcome, "status %d", code)
		if want == basev0.ProbeOutcome_PROBE_OUTCOME_FAILED {
			require.Equal(t, basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNEXPECTED_STATUS, result.FailureKind)
			require.Contains(t, result.Message, fmt.Sprintf("answered %d", code))
		}
	}

	degraded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("starting"))
	}))
	t.Cleanup(degraded.Close)
	result := readiness.Check(ctx, requirement(t, standards.HTTP, standards.HTTP, probe),
		readiness.Target{Address: strings.TrimPrefix(degraded.URL, "http://")})
	require.Equal(t, basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNEXPECTED_BODY, result.FailureKind)
}

// TestOpenAdminPortDoesNotMaskAClosedGrpcEndpoint is the audit case: an open
// port on one endpoint used to be enough to call the whole dependency ready.
func TestOpenAdminPortDoesNotMaskAClosedGrpcEndpoint(t *testing.T) {
	ctx := context.Background()
	service, err := resources.LoadServiceFromDir(ctx, "../resources/testdata/readiness/generated")
	require.NoError(t, err)
	service.WithModule("saas")
	endpoints, err := service.LoadEndpoints(ctx)
	require.NoError(t, err)

	dependency := &resources.ServiceDependency{
		Name:   "accounts",
		Module: "saas",
		Endpoints: []*resources.EndpointReference{
			{Name: standards.GRPC},
			{Name: "admin"},
		},
	}
	requirements, err := resources.PlanServiceDependencyReadiness(dependency, endpoints)
	require.NoError(t, err)
	require.Len(t, requirements, 2)

	admin, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	closedGRPC := listenAndClose(t)

	report := readiness.Evaluate(ctx, requirements, 7, func(r *resources.ReadinessRequirement) readiness.Target {
		if r.Endpoint == "admin" {
			return readiness.Target{Address: admin.Addr().String(), Started: true}
		}
		return readiness.Target{Address: closedGRPC, Started: true}
	})

	require.False(t, report.Ready)
	require.Equal(t, uint64(7), report.Generation)

	failing := resources.FailingPredicates(report)
	require.Len(t, failing, 1)
	require.Contains(t, failing[0], `saas/accounts/grpc: grpc health SERVING for "accounts.v1.Accounts"`)
	require.Contains(t, failing[0], closedGRPC)
}

func TestEndpointlessDependenciesGateOnLifecycleAndCompletion(t *testing.T) {
	ctx := context.Background()

	unfinished := &resources.JobDependency{Name: "migrate", Module: "saas"}
	job, err := resources.PlanJobDependencyReadiness(unfinished)
	require.NoError(t, err)

	result := readiness.Check(ctx, job, readiness.Target{Started: true})
	require.Equal(t, basev0.ProbeFailureKind_PROBE_FAILURE_KIND_INCOMPLETE, result.FailureKind)
	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_PASSED, readiness.Check(ctx, job, readiness.Target{Completed: true}).Outcome)

	// A long-running dependency that died after Start loses readiness rather
	// than keeping the verdict its successful Start earned.
	service := &resources.ServiceDependency{Name: "worker", Module: "saas"}
	requirements, err := resources.PlanServiceDependencyReadiness(service, nil)
	require.NoError(t, err)
	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_PASSED, readiness.Check(ctx, requirements[0], readiness.Target{Started: true}).Outcome)

	dead := readiness.Check(ctx, requirements[0], readiness.Target{Started: false})
	require.Equal(t, basev0.ProbeFailureKind_PROBE_FAILURE_KIND_LIFECYCLE, dead.FailureKind)
}

func TestLegacyEndpointsStillCheckTransport(t *testing.T) {
	ctx := context.Background()
	service, err := resources.LoadServiceFromDir(ctx, "../resources/testdata/readiness/customized")
	require.NoError(t, err)
	service.WithModule("saas")
	endpoints, err := service.LoadEndpoints(ctx)
	require.NoError(t, err)

	dependency := &resources.ServiceDependency{Name: "payments", Module: "saas"}
	requirements, err := resources.PlanServiceDependencyReadiness(dependency, endpoints)
	require.NoError(t, err)

	// A customized server with no Health service is never asked for one; it is
	// held to the transport check it has always been held to, and the report
	// says the predicate is the legacy one.
	alive := serveGRPC(t, false, healthpb.HealthCheckResponse_SERVING)
	report := readiness.Evaluate(ctx, requirements, 1, func(*resources.ReadinessRequirement) readiness.Target {
		return readiness.Target{Address: alive}
	})
	require.True(t, report.Ready)
	for _, result := range report.Results {
		require.Contains(t, result.Predicate, "legacy: endpoint declares no readiness")
	}

	report = readiness.Evaluate(ctx, requirements, 2, func(*resources.ReadinessRequirement) readiness.Target {
		return readiness.Target{Address: listenAndClose(t)}
	})
	require.False(t, report.Ready)
	require.Len(t, resources.FailingPredicates(report), 2)
}
