package otel_test

import (
	"context"
	"net"
	"testing"
	"time"

	wooltel "github.com/codefly-dev/core/wool/otel"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

// These tests make a real RPC over a real connection between a server built from
// GRPCServerOptions and a client built from GRPCDialOptions, because the defect
// they cover is invisible anywhere else: both ends read the GLOBAL text-map
// propagator, and with its no-op default every assertion that stays inside one
// process still passes while no service-to-service trace survives a hop.

// observedCall is what the server end saw: the metadata that arrived on the
// wire, the span the instrumentation handed the handler, and the baggage that
// reached it.
type observedCall struct {
	metadata metadata.MD
	span     oteltrace.SpanContext
	baggage  baggage.Baggage
}

// instrumentedRoundTrip makes one RPC from an instrumented client to an
// instrumented server and reports what the server observed. ctx is the caller's,
// carrying whatever it wants propagated.
//
// Loopback TCP rather than a Unix socket: a temp-dir socket path exceeds
// sun_path's 104-byte limit on Darwin once the test name is in it.
func instrumentedRoundTrip(t *testing.T, ctx context.Context) observedCall {
	t.Helper()

	var observed observedCall
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer(wooltel.GRPCServerOptions()...)
	healthpb.RegisterHealthServer(server, &recordingHealth{
		Server: health.NewServer(),
		onCall: func(served context.Context) {
			observed.metadata, _ = metadata.FromIncomingContext(served)
			observed.span = oteltrace.SpanFromContext(served).SpanContext()
			observed.baggage = baggage.FromContext(served)
		},
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(
		listener.Addr().String(),
		append(
			[]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
			wooltel.GRPCDialOptions()...,
		)...,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(call, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)

	return observed
}

// THE ACCEPTANCE OF #712. Without a global propagator the client injects no
// traceparent, the server extracts none, and the server's span opens a trace of
// its own — so two instrumented services produce two disconnected traces for one
// request, which is the state this asserts against.
func TestAnInstrumentedHopContinuesTheCallersTrace(t *testing.T) {
	realBackend(t, "continuing-trace")

	caller, span := otel.Tracer("caller").Start(context.Background(), "Call")
	defer span.End()
	callerSpan := oteltrace.SpanFromContext(caller).SpanContext()
	require.True(t, callerSpan.IsValid(), "the caller must have a span to propagate")

	observed := instrumentedRoundTrip(t, caller)

	require.True(t, observed.span.IsValid(), "the server instrumentation must have started a span")
	require.Equal(t, callerSpan.TraceID(), observed.span.TraceID(),
		"the server span must join the caller's trace rather than start a new one")
	require.NotEqual(t, callerSpan.SpanID(), observed.span.SpanID(),
		"while being its own span within that trace")
	// The sampled flag travels in traceparent too, and the server's default
	// sampler is parent-based: a decision that did not cross would leave the
	// server recording nothing for a request the caller sampled.
	require.True(t, observed.span.IsSampled(), "the caller's sampling decision must cross")

	// Read on the wire, not inferred from the span: the header is what a hop to a
	// service outside this process carries. Its span id is the client span
	// otelgrpc starts for the RPC, a child of the caller's, so it is pinned by
	// shape rather than by value.
	require.Len(t, observed.metadata.Get("traceparent"), 1, "exactly one traceparent")
	require.Regexp(t,
		`^00-`+callerSpan.TraceID().String()+`-[0-9a-f]{16}-0[0-9a-f]$`,
		observed.metadata.Get("traceparent")[0])
}

// The other half of the acceptance: the trace crosses and baggage does not.
// Baggage is caller-supplied key/value data, and forwarding it across a trust
// boundary is a decision this package does not make for a service — so an
// otherwise fully propagating hop must leave it behind.
func TestAnInstrumentedHopDoesNotForwardBaggage(t *testing.T) {
	realBackend(t, "no-baggage")

	member, err := baggage.NewMember("tenant", "acme")
	require.NoError(t, err)
	bag, err := baggage.New(member)
	require.NoError(t, err)
	caller, span := otel.Tracer("caller").Start(
		baggage.ContextWithBaggage(context.Background(), bag), "Call")
	defer span.End()
	require.Equal(t, 1, baggage.FromContext(caller).Len(), "the caller must hold baggage to forward")

	observed := instrumentedRoundTrip(t, caller)

	require.Len(t, observed.metadata.Get("traceparent"), 1, "the trace still crosses")
	require.Empty(t, observed.metadata.Get("baggage"), "no baggage on the wire")
	require.Zero(t, observed.baggage.Len(), "and none reaching the handler")
}

// The configuration behind both wire tests. Those prove one carrier on one
// transport; this pins what every carrier gets, so a later composite that adds
// Baggage fails here rather than in a service that has already forwarded a
// caller's data.
func TestEnableInstallsTraceContextAlone(t *testing.T) {
	realBackend(t, "propagator-fields")

	require.Equal(t, []string{"traceparent", "tracestate"}, otel.GetTextMapPropagator().Fields())
}
