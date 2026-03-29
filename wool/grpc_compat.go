package wool

import (
	"context"
	"net/http"

	"google.golang.org/grpc/metadata"
)

// GRPC returns a GRPC helper for metadata propagation.
// Backwards-compatible wrapper.
type GRPC struct {
	w *Wool
}

func (w *Wool) GRPC() *GRPC {
	return &GRPC{w: w}
}

// Inject reads gRPC incoming metadata and injects known keys into the Wool context.
func (g *GRPC) Inject() {
	ctx := g.w.Context()
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return
	}
	for _, key := range ContextKeys {
		values := md.Get(string(key))
		if len(values) > 0 {
			g.w.with(key, values[0])
		}
	}
}

// MetadataFromRequest extracts gRPC metadata from an HTTP request,
// mapping known HTTP headers to context keys.
func MetadataFromRequest(_ context.Context, req *http.Request) metadata.MD {
	md := metadata.New(map[string]string{})
	for header, key := range HTTPMappings {
		values := req.Header.Values(header)
		if len(values) > 0 {
			md.Set(string(key), values...)
		}
	}
	return md
}

// GRPCInstrumentation returns gRPC server options for instrumentation.
func GRPCInstrumentation() []interface{} {
	return nil
}
