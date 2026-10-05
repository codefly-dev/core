// Package wool is a lightweight, pluggable telemetry layer for codefly.
//
// Loggers ("wools") attach to a context with structured fields and a
// hierarchy ("module/component/method"); messages are routed through a
// pluggable Provider that ships them to stdout, gRPC, OpenTelemetry, or
// any custom sink. Levels in increasing severity are TRACE, DEBUG, INFO,
// FOCUS, WARN, ERROR — FOCUS is a highlighted milestone shown at INFO and
// above. Per-scope level overrides come from CODEFLY_LOG (see SetLogScopes).
//
// Typical usage:
//
//	w := wool.Get(ctx).In("MyMethod", wool.Field("user", id))
//	w.Trace("starting work")
//	if err := doThing(); err != nil {
//	    return w.Wrapf(err, "could not do thing")
//	}
//	w.Info("done")
//
// Log/trace correlation:
//
// When a telemetry backend is active and reports span identity (see
// SpanIdentity), every log record emitted inside a span carries that span's
// trace_id and span_id — on the rendered line and in the JSON a sink marshals.
// That is deliberately the join key for the deployment shape where the two
// signals leave a container by different routes: logs are written to stdout and
// collected from the node, traces are exported over OTLP, and the ids are the
// only thing that lets a reader move from a slow trace to the lines it
// produced. A process with no telemetry has no span, so its output is
// unchanged.
//
// Core packages and their use:
//
//   - wool — log levels, fields, error wrapping (this package)
//   - wool/log — stdout / formatted log provider
//   - wool/grpc — gRPC log streaming provider
//   - wool/otel — OpenTelemetry provider
package wool
