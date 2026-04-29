// Package wool is a lightweight, pluggable telemetry layer for codefly.
//
// Loggers ("wools") attach to a context with structured fields and a
// hierarchy ("module/component/method"); messages are routed through a
// pluggable Provider that ships them to stdout, gRPC, OpenTelemetry, or
// any custom sink. Levels are DEBUG, TRACE, FOCUS, INFO, WARN, ERROR.
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
