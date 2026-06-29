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
// Core packages and their use:
//
//   - wool — log levels, fields, error wrapping (this package)
//   - wool/log — stdout / formatted log provider
//   - wool/grpc — gRPC log streaming provider
//   - wool/otel — OpenTelemetry provider
package wool
