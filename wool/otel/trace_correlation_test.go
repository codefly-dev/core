package otel_test

import (
	"context"
	"net"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/codefly-dev/core/wool"
	wooltel "github.com/codefly-dev/core/wool/otel"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// These tests drive the real OpenTelemetry SDK and, for the RPC case, a real
// gRPC server over a Unix socket with this repository's own instrumentation.
// Nothing here fakes a tracer: the repository rule is "Never mock. Tests use
// real infrastructure. If a boundary is hard to reach, reach it anyway", and the
// first version of this change passed its fakes while being broken on every real
// path — the fakes went through wool.StartSpan, which is the one path that
// already worked.

var (
	traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	spanIDPattern  = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// capture records the Log records a sink receives, so a test can assert on what
// was actually emitted rather than on what a span reports.
type capture struct {
	mu   sync.Mutex
	logs []*wool.Log
}

func (c *capture) Process(msg *wool.Log) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logs = append(c.logs, msg)
}

func (c *capture) only(t *testing.T) *wool.Log {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	require.Len(t, c.logs, 1, "expected exactly one captured log record")
	return c.logs[0]
}

// realBackend enables the real OTEL backend. WithStdout keeps export off the
// network; tests that care about export capture stdout explicitly.
func realBackend(t *testing.T, name string) *wooltel.Provider {
	t.Helper()
	// Enable replaces TWO global registries — OTEL's tracer provider and wool's
	// telemetry provider — and Shutdown restores neither. Leaving them pointing
	// at a shut-down backend means a later ordinary wool.New sees telemetry
	// enabled and starts an invalid span from it. Restoring both is part of the
	// fixture, and these tests stay serial (no t.Parallel) because the state
	// they mutate is process-wide.
	previousTracerProvider := otel.GetTracerProvider()
	previousTelemetry := wool.GetTelemetry()
	backend, err := wooltel.Enable(wooltel.WithStdout(), wooltel.WithServiceName(name))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = backend.Shutdown(context.Background())
		otel.SetTracerProvider(previousTracerProvider)
		wool.RegisterTelemetry(previousTelemetry)
	})
	return backend
}

// The fixture's own contract: after cleanup, an ordinary provider must not be
// handed a shut-down backend. Asserting the OBSERVABLE consequence rather than
// pointer equality, because OTEL's global starts as a delegating provider and
// comparing pointers is not its lifecycle contract.
func TestTheFixtureLeavesNoBackendBehind(t *testing.T) {
	require.Nil(t, wool.GetTelemetry(), "a prior test must not have left telemetry registered")

	t.Run("inner", func(t *testing.T) { realBackend(t, "inner") })

	require.Nil(t, wool.GetTelemetry(),
		"cleanup must restore wool's registry, or a later wool.New starts spans on a dead backend")
	require.False(t, wool.TelemetryEnabled())
}

// THE REGRESSION THIS CHANGE WAS REOPENED FOR.
//
// An incoming RPC's span is created by the otelgrpc stats handler that
// GRPCServerOptions installs, and it lives in OpenTelemetry's context slot —
// not in wool's. A handler that logs through wool.Get(ctx) previously emitted no
// ids at all despite a live, valid, recording server span, which is every real
// service in this fleet.
func TestALogInsideAnIncomingInstrumentedRPCCarriesTheServerSpan(t *testing.T) {
	backend := realBackend(t, "incoming-rpc")
	sink := &capture{}
	provider := wool.New(context.Background(), &wool.Resource{Kind: "test", Unique: "incoming-rpc"}).
		WithLogger(sink).
		WithTelemetry(backend)

	// The identity the handler observed, read from OTEL itself inside the call.
	var observed oteltrace.SpanContext
	// Loopback TCP rather than a Unix socket: a temp-dir socket path exceeds
	// sun_path's 104-byte limit on Darwin once the test name is in it. Still a
	// real server, a real connection and the real stats handler.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer(wooltel.GRPCServerOptions()...)
	healthpb.RegisterHealthServer(server, &recordingHealth{
		Server: health.NewServer(),
		onCall: func(ctx context.Context) {
			observed = oteltrace.SpanFromContext(ctx).SpanContext()
			// Exactly what a handler does: no wool span of its own.
			wool.Get(provider.Inject(ctx)).Info("handling request")
		},
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)

	require.True(t, observed.IsValid(), "the instrumentation must have started a server span")
	record := sink.only(t)
	require.Equal(t, observed.TraceID().String(), record.TraceID,
		"the log must name the server span's trace")
	require.Equal(t, observed.SpanID().String(), record.SpanID,
		"the log must name the server span itself")
}

type recordingHealth struct {
	*health.Server
	onCall func(context.Context)
}

func (h *recordingHealth) Check(
	ctx context.Context,
	request *healthpb.HealthCheckRequest,
) (*healthpb.HealthCheckResponse, error) {
	h.onCall(ctx)
	return h.Server.Check(ctx, request)
}

// The second half of the same defect: a span nested on the backend from a wool
// span's context must be attributed to ITSELF, not to the enclosing wool span.
// Omission is visible; a confidently wrong span id is not.
func TestALogInsideANestedBackendSpanNamesThatSpanNotItsParent(t *testing.T) {
	backend := realBackend(t, "nested-span")
	sink := &capture{}
	provider := wool.New(context.Background(), &wool.Resource{Kind: "test", Unique: "nested-span"}).
		WithLogger(sink).
		WithTelemetry(backend)

	parent, end := wool.StartSpan(provider.Inject(context.Background()), "Parent")
	defer end()
	parentIdentity := oteltrace.SpanFromContext(parent.Context()).SpanContext()
	require.True(t, parentIdentity.IsValid())

	childCtx, child := backend.NewTracer("test").Start(parent.Context(), "Child")
	defer child.End()
	childIdentity := oteltrace.SpanFromContext(childCtx).SpanContext()
	require.True(t, childIdentity.IsValid())
	require.NotEqual(t, parentIdentity.SpanID(), childIdentity.SpanID(), "the child must be its own span")

	wool.Get(provider.Inject(childCtx)).WithLogger(sink).Info("deeper")

	record := sink.only(t)
	require.Equal(t, childIdentity.SpanID().String(), record.SpanID)
	require.NotEqual(t, parentIdentity.SpanID().String(), record.SpanID)
	require.Equal(t, childIdentity.TraceID().String(), record.TraceID,
		"both spans share one trace")
}

// The on-wire join: the ids on the log must match the ids on the span an actual
// exporter received. Comparing against the live SpanContext proves agreement
// with the SDK in memory; this proves agreement with what a backend is handed,
// which is what a reader follows.
func TestTheLoggedIDsMatchTheExportedSpan(t *testing.T) {
	sink := &capture{}
	// A real SpanExporter behind a SimpleSpanProcessor, so the span travels an
	// actual export pipeline. tracetest.NewSpanRecorder is a SpanProcessor whose
	// ForceFlush is a no-op: it records the SDK's OnEnd snapshot and proves
	// nothing was exported.
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)),
		sdktrace.WithResource(resource.Empty()),
	)
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })

	ctx, span := tracerProvider.Tracer("test").Start(context.Background(), "Operation")
	provider := wool.New(context.Background(), &wool.Resource{Kind: "test", Unique: "exported"}).
		WithLogger(sink).
		WithTelemetry(realBackend(t, "exported"))
	wool.Get(provider.Inject(ctx)).WithLogger(sink).Info("handling request")
	span.End()
	require.NoError(t, tracerProvider.ForceFlush(context.Background()))

	ended := exporter.GetSpans().Snapshots()
	require.Len(t, ended, 1, "the exporter must have received the span")
	record := sink.only(t)
	require.Equal(t, ended[0].SpanContext().TraceID().String(), record.TraceID)
	require.Equal(t, ended[0].SpanContext().SpanID().String(), record.SpanID)
	require.Regexp(t, traceIDPattern, record.TraceID)
	require.Regexp(t, spanIDPattern, record.SpanID)
}

// MAJOR 1 (round 2). A reader that participates is authoritative even when it
// answers with nothing: a line whose span was deliberately detached must not
// regain the enclosing wool span's trace through the fallback. Other request
// context values are kept, so this is a cleared SPAN, not a fresh context.
func TestAClearedBackendContextDoesNotResurrectTheParentSpan(t *testing.T) {
	backend := realBackend(t, "cleared")
	sink := &capture{}
	provider := wool.New(context.Background(), &wool.Resource{Kind: "test", Unique: "cleared"}).
		WithLogger(sink).
		WithTelemetry(backend)

	parent, end := wool.StartSpan(provider.Inject(context.Background()), "Parent")
	defer end()
	require.True(t, oteltrace.SpanFromContext(parent.Context()).SpanContext().IsValid())

	type marker struct{}
	kept := context.WithValue(parent.Context(), marker{}, "retained")
	cleared := oteltrace.ContextWithSpanContext(kept, oteltrace.SpanContext{})
	require.False(t, oteltrace.SpanFromContext(cleared).SpanContext().IsValid())
	require.Equal(t, "retained", cleared.Value(marker{}), "only the span was detached")

	wool.Get(provider.Inject(cleared)).WithLogger(sink).Info("detached")

	record := sink.only(t)
	require.Empty(t, record.TraceID, "a detached line must not carry the parent's trace")
	require.Empty(t, record.SpanID)
}

// MAJOR 2 (round 2). A providerless handle reads the global registry on every
// line, so registration concurrent with logging must not race. Run under -race.
func TestRegisteringTelemetryWhileLoggingDoesNotRace(t *testing.T) {
	previous := wool.GetTelemetry()
	t.Cleanup(func() { wool.RegisterTelemetry(previous) })

	// Built BEFORE any registration, so every line takes the global branch.
	handle := wool.Get(context.Background()).WithLogger(&capture{})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			handle.Info("logging while telemetry is reconfigured")
		}
	}()
	go func() {
		defer wg.Done()
		backend, err := wooltel.Enable(wooltel.WithStdout(), wooltel.WithServiceName("racing"))
		require.NoError(t, err)
		for i := 0; i < 500; i++ {
			wool.RegisterTelemetry(backend)
			wool.RegisterTelemetry(nil)
		}
		_ = backend.Shutdown(context.Background())
	}()
	wg.Wait()
}

// The corrected contract: validity, not recording. An unsampled span is VALID
// and NOT recording, exports nothing, and its ids are still real — they join
// this line to a service that did sample the same trace. The earlier prose
// promised empty ids here, which the adapter never did.
func TestAnUnsampledSpanStillReportsItsRealIDs(t *testing.T) {
	backend := realBackend(t, "unsampled")
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.NeverSample()),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)),
	)
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })

	ctx, span := tracerProvider.Tracer("test").Start(context.Background(), "Unsampled")
	require.False(t, span.IsRecording(), "NeverSample must not record")
	require.True(t, span.SpanContext().IsValid(), "but the context is still valid")

	traceID, spanID := backend.SpanIdentityFromContext(ctx)
	require.Equal(t, span.SpanContext().TraceID().String(), traceID)
	require.Equal(t, span.SpanContext().SpanID().String(), spanID)

	span.End()
	require.NoError(t, tracerProvider.ForceFlush(context.Background()))
	require.Empty(t, exporter.GetSpans().Snapshots(),
		"an unsampled span exports nothing, and its ids are still worth stamping")
}

// An ended span keeps its ids and stops recording — same rule.
func TestAnEndedSpanStillReportsItsRealIDs(t *testing.T) {
	backend := realBackend(t, "ended")
	ctx, span := backend.NewTracer("test").Start(context.Background(), "Ended")
	identity := oteltrace.SpanFromContext(ctx).SpanContext()
	span.End()

	traceID, spanID := backend.SpanIdentityFromContext(ctx)
	require.Equal(t, identity.TraceID().String(), traceID)
	require.Equal(t, identity.SpanID().String(), spanID)
}

// An absent identity must report nothing rather than an all-zero id, which is a
// well-formed id for a trace that never existed.
func TestAContextWithNoSpanReportsNoIDs(t *testing.T) {
	backend := realBackend(t, "no-span")

	traceID, spanID := backend.SpanIdentityFromContext(context.Background())

	require.Empty(t, traceID)
	require.Empty(t, spanID)
	require.False(t, oteltrace.SpanFromContext(context.Background()).SpanContext().IsValid())
}

func TestANilContextReportsNoIDs(t *testing.T) {
	backend := realBackend(t, "nil-ctx")

	traceID, spanID := backend.SpanIdentityFromContext(nil) //nolint:staticcheck // the point is that it does not panic

	require.Empty(t, traceID)
	require.Empty(t, spanID)
}
