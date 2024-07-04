package wool

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/metadata"
)

// HeaderKey sanitizes the header name to be used in metadata
// Append wool:
// Lower case
// Suppress X-Codefly
func HeaderKey(header string) string {
	header = strings.ToLower(header)
	header = strings.ReplaceAll(header, "-", ".")
	if codeflyHeader, ok := strings.CutPrefix(header, "x.codefly."); ok {
		return fmt.Sprintf("codefly.%s", codeflyHeader)
	}
	return header
}

func InjectMetadata(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.New(map[string]string{})
	}
	for key, values := range md {
		switch ContextKey(key) {
		case UserAuthIDKey:
			ctx = context.WithValue(ctx, UserAuthIDKey, values[0])
		case UserEmailKey:
			ctx = context.WithValue(ctx, UserEmailKey, values[0])
		case UserNameKey:
			ctx = context.WithValue(ctx, UserNameKey, values[0])
		case UserGivenNameKey:
			ctx = context.WithValue(ctx, UserGivenNameKey, values[0])
		}
	}
	return metadata.NewIncomingContext(ctx, md)
}

func GRPCOut(ctx context.Context) context.Context {
	// Add Metadata from gRPC
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	return ctx
}
