package grpc

import (
	"context"
	"net/http"

	"github.com/codefly-dev/core/wool"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GRPC provides gRPC metadata propagation for user identity.
type GRPC struct {
	w *wool.Wool
}

// FromWool creates a GRPC helper from a Wool instance.
func FromWool(w *wool.Wool) *GRPC {
	return &GRPC{w: w}
}

// MetadataFromRequest extracts gRPC metadata from an HTTP request,
// mapping known HTTP headers to context keys.
func MetadataFromRequest(ctx context.Context, req *http.Request) metadata.MD {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.New(map[string]string{})
	}
	for header, values := range req.Header {
		if key, known := wool.HTTPMappings[header]; known {
			md.Set(string(key), values...)
		}
	}
	return md
}

// Inject reads gRPC incoming metadata and injects known keys into the Wool context.
func (g *GRPC) Inject() {
	ctx := g.w.Context()
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return
	}
	for _, key := range wool.ContextKeys {
		values := md.Get(string(key))
		if len(values) > 0 {
			g.w.WithUserAuthID(values[0]) // generic set via context
		}
	}
}

// Metadata returns the incoming gRPC metadata from the context.
func (g *GRPC) Metadata() metadata.MD {
	md, ok := metadata.FromIncomingContext(g.w.Context())
	if !ok {
		return metadata.New(map[string]string{})
	}
	return md
}

// OutgoingContext returns a context with outgoing gRPC metadata
// populated from the Wool context keys.
func (g *GRPC) OutgoingContext() context.Context {
	ctx := g.w.Context()
	md := metadata.New(map[string]string{})
	for _, key := range wool.ContextKeys {
		if value, ok := ctx.Value(key).(string); ok {
			md.Set(string(key), value)
		}
	}
	return metadata.NewOutgoingContext(ctx, md)
}

// IsNotFound checks if a gRPC error has code NotFound.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	return st.Code() == codes.NotFound
}

// IsUnauthorized checks if a gRPC error has code Unauthenticated.
func IsUnauthorized(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	return st.Code() == codes.Unauthenticated
}
