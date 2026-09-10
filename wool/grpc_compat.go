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

// WorkContextHeader carries a signed Work Context (sdk-go's
// WorkContextHeaderName). It is the only HTTP carrier for one, and a service
// that authenticates by Work Context reads it from gRPC metadata under the
// header's own name — so the REST ingress must forward it verbatim, not under a
// rewritten context key like the identity headers above.
const WorkContextHeader = "X-Codefly-Work-Context"

// workContextMetadataKey is the metadata key the sdk reads the Work Context
// from: the header name, lowercased as gRPC metadata keys are.
const workContextMetadataKey = "x-codefly-work-context"

// MetadataFromRequest extracts gRPC metadata from an HTTP request, mapping known
// HTTP headers to context keys and forwarding the Work Context header verbatim.
//
// Without the latter, every agent-generated REST server (which installs this as
// its grpc-gateway metadata annotator) silently dropped the caller's Work
// Context, so a module that authenticates by it answered Unauthenticated to
// every REST caller however correct the request (#435).
func MetadataFromRequest(_ context.Context, req *http.Request) metadata.MD {
	md := metadata.New(map[string]string{})
	for header, key := range HTTPMappings {
		values := req.Header.Values(header)
		if len(values) > 0 {
			md.Set(string(key), values...)
		}
	}
	if values := req.Header.Values(WorkContextHeader); len(values) > 0 {
		md.Set(workContextMetadataKey, values...)
	}
	return md
}

// GRPCInstrumentation returns gRPC server options for instrumentation.
func GRPCInstrumentation() []interface{} {
	return nil
}
