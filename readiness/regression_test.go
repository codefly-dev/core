package readiness_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/stats"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/readiness"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
)

func secureRequirement(t *testing.T, api string, probe *resources.Probe, secured bool) *resources.ReadinessRequirement {
	t.Helper()
	wire, err := probe.Proto(api)
	require.NoError(t, err)
	return &resources.ReadinessRequirement{
		Dependency: "saas/accounts", Endpoint: api, API: api, Probe: wire, Secured: secured, Declared: true,
	}
}

// A probe asks about the resource it names. Following a redirect answered about
// a different one: a /healthz redirecting to an unrelated 200 passed as healthy.
func TestHTTPProbeDoesNotFollowRedirects(t *testing.T) {
	ctx := context.Background()
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("unrelated login page"))
	}))
	t.Cleanup(elsewhere.Close)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	t.Cleanup(redirecting.Close)

	target := readiness.Target{Address: strings.TrimPrefix(redirecting.URL, "http://")}

	strict := secureRequirement(t, standards.HTTP, &resources.Probe{Kind: resources.ProbeKindHTTP, Path: "/healthz", Statuses: []string{"200"}}, false)
	result := readiness.Check(ctx, strict, target)
	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_FAILED, result.Outcome)
	require.Equal(t, basev0.ProbeFailureKind_PROBE_FAILURE_KIND_UNEXPECTED_STATUS, result.FailureKind)
	require.Contains(t, result.Message, "answered 302")

	// The 3xx is now observable, so the default 200-399 range can actually match
	// one instead of never seeing it.
	lenient := secureRequirement(t, standards.HTTP, &resources.Probe{Kind: resources.ProbeKindHTTP, Path: "/healthz"}, false)
	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_PASSED, readiness.Check(ctx, lenient, target).Outcome)
}

// A secured endpoint answers TLS only; dialling it plaintext produced a
// transport error indistinguishable from a dead service, forever.
func TestSecuredRequirementsDialTLS(t *testing.T) {
	ctx := context.Background()

	plaintextGRPC := serveGRPC(t, true, healthpb.HealthCheckResponse_SERVING)
	probe := &resources.Probe{Kind: resources.ProbeKindGRPCHealth}

	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_PASSED,
		readiness.Check(ctx, secureRequirement(t, standards.GRPC, probe, false), readiness.Target{Address: plaintextGRPC}).Outcome,
		"an unsecured requirement must speak plaintext")

	secured := readiness.Check(ctx, secureRequirement(t, standards.GRPC, probe, true), readiness.Target{Address: plaintextGRPC})
	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_FAILED, secured.Outcome,
		"a secured requirement must attempt TLS, which a plaintext server cannot answer")

	plaintextHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(plaintextHTTP.Close)
	address := strings.TrimPrefix(plaintextHTTP.URL, "http://")
	httpProbe := &resources.Probe{Kind: resources.ProbeKindHTTP, Path: "/healthz"}

	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_PASSED,
		readiness.Check(ctx, secureRequirement(t, standards.HTTP, httpProbe, false), readiness.Target{Address: address}).Outcome)
	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_FAILED,
		readiness.Check(ctx, secureRequirement(t, standards.HTTP, httpProbe, true), readiness.Target{Address: address}).Outcome,
		"a secured requirement must request https, which a plaintext server cannot answer")
}

// An unusable address never resolves by waiting, so it must not be reported as
// a transient network failure a caller will retry forever.
func TestUnusableTargetsAreNotReportedAsUnreachable(t *testing.T) {
	ctx := context.Background()
	for name, probe := range map[string]*resources.Probe{
		"grpc":      {Kind: resources.ProbeKindGRPCHealth},
		"http":      {Kind: resources.ProbeKindHTTP, Path: "/healthz"},
		"transport": {Kind: resources.ProbeKindTransport},
	} {
		t.Run(name, func(t *testing.T) {
			api := standards.GRPC
			if name != "grpc" {
				api = standards.HTTP
			}
			if name == "transport" {
				api = standards.TCP
			}
			result := readiness.Check(ctx, secureRequirement(t, api, probe, false), readiness.Target{})
			require.Equal(t, basev0.ProbeFailureKind_PROBE_FAILURE_KIND_INVALID_TARGET, result.FailureKind)
		})
	}
}

// Declared timing is binding: a boot window expressed as a failure threshold
// used to be discarded and the probe failed on its first attempt.
func TestDeclaredTimingIsHonoured(t *testing.T) {
	ctx := context.Background()

	var mu sync.Mutex
	attempts := 0
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		attempts++
		booted := attempts > 2
		mu.Unlock()
		if !booted {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(flaky.Close)
	address := strings.TrimPrefix(flaky.URL, "http://")

	tolerant := secureRequirement(t, standards.HTTP, &resources.Probe{
		Kind: resources.ProbeKindHTTP, Path: "/healthz",
		Period: "1ms", FailureThreshold: 5,
	}, false)
	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_PASSED, readiness.Check(ctx, tolerant, readiness.Target{Address: address}).Outcome,
		"a probe declaring 5 tolerated failures must not give up on the first 503")

	mu.Lock()
	attempts = 0
	mu.Unlock()
	impatient := secureRequirement(t, standards.HTTP, &resources.Probe{Kind: resources.ProbeKindHTTP, Path: "/healthz"}, false)
	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_FAILED, readiness.Check(ctx, impatient, readiness.Target{Address: address}).Outcome,
		"a probe declaring no timing is still a single attempt")

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(healthy.Close)
	delayed := secureRequirement(t, standards.HTTP, &resources.Probe{
		Kind: resources.ProbeKindHTTP, Path: "/healthz", InitialDelay: "80ms",
	}, false)
	started := time.Now()
	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_PASSED,
		readiness.Check(ctx, delayed, readiness.Target{Address: strings.TrimPrefix(healthy.URL, "http://")}).Outcome)
	require.GreaterOrEqual(t, time.Since(started), 80*time.Millisecond, "the declared initial delay must be waited out")
}

type connectionCounter struct {
	mu    sync.Mutex
	count int
}

func (c *connectionCounter) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}
func (c *connectionCounter) HandleRPC(context.Context, stats.RPCStats) {}
func (c *connectionCounter) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

func (c *connectionCounter) HandleConn(_ context.Context, s stats.ConnStats) {
	if _, ok := s.(*stats.ConnBegin); ok {
		c.mu.Lock()
		c.count++
		c.mu.Unlock()
	}
}

// A short-period probe opened and tore down an HTTP/2 connection per tick.
func TestGrpcProbeReusesOneConnectionAcrossItsSchedule(t *testing.T) {
	counter := &connectionCounter{}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer(grpc.StatsHandler(counter))
	checker := health.NewServer()
	checker.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, checker)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	probe := &resources.Probe{Kind: resources.ProbeKindGRPCHealth, Period: "1ms", SuccessThreshold: 4}
	result := readiness.Check(context.Background(), secureRequirement(t, standards.GRPC, probe, false),
		readiness.Target{Address: listener.Addr().String()})
	require.Equal(t, basev0.ProbeOutcome_PROBE_OUTCOME_PASSED, result.Outcome)

	counter.mu.Lock()
	defer counter.mu.Unlock()
	require.Equal(t, 1, counter.count, "four attempts must share one connection")
}

// One slow dependency used to set the floor for the whole pass.
func TestEvaluateChecksRequirementsConcurrently(t *testing.T) {
	probe := &resources.Probe{Kind: resources.ProbeKindTransport, InitialDelay: "150ms"}
	var requirements []*resources.ReadinessRequirement
	for range 4 {
		requirements = append(requirements, secureRequirement(t, standards.TCP, probe, false))
	}

	started := time.Now()
	report := readiness.Evaluate(context.Background(), requirements, 1, func(*resources.ReadinessRequirement) readiness.Target {
		return readiness.Target{Address: closedAddress}
	})
	elapsed := time.Since(started)

	require.False(t, report.Ready)
	require.Len(t, report.Results, 4)
	require.Less(t, elapsed, 500*time.Millisecond, "four 150ms delays run in parallel, not one after another")
}
