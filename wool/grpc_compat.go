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
//
// This constant is the single source of that name: metadata.MD.Set lowercases
// the key it is given, so handing this header to Set derives the metadata key
// the sdk reads. Never restate the lowercased form as a second constant — the
// two drift apart on a rename and silently drop the header again (#435).
const WorkContextHeader = "X-Codefly-Work-Context"

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
	// A Work Context is one capability bound to one audience, so exactly one
	// value is meaningful. A repeated header — a caller's own alongside the one
	// a trusted proxy minted, including a value smuggled in under
	// grpc-gateway's Grpc-Metadata- prefix, which metadata.Join places ahead of
	// ours — leaves which capability authorizes the call to whichever index the
	// verifier happens to read. That is ambiguous, so it fails closed here
	// rather than resolving to a caller-chosen credential. An empty value is
	// not a credential either.
	if values := req.Header.Values(WorkContextHeader); len(values) == 1 && values[0] != "" {
		md.Set(WorkContextHeader, values[0])
	}
	return md
}

// GRPCInstrumentation returns gRPC server options for instrumentation.
func GRPCInstrumentation() []interface{} {
	return nil
}
