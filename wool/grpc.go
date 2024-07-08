package wool

import (
	"context"
	"net/http"

	"google.golang.org/grpc/metadata"
)

type GRPC struct {
	w *Wool
}

func MetadataFromRequest(ctx context.Context, req *http.Request) metadata.MD {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.New(map[string]string{})
	}
	for header, values := range req.Header {
		if key, known := HTTPMappings[header]; known {
			md.Set(string(key), values...)
		}
	}
	return md
}

func (grpc *GRPC) Inject() *Wool {
	md, ok := metadata.FromIncomingContext(grpc.w.ctx)
	if !ok {
		md = metadata.New(map[string]string{})
	}
	for _, key := range ContextKeys {
		values := md.Get(string(key))
		if len(values) > 0 {
			grpc.w.ctx = context.WithValue(grpc.w.ctx, key, values[0])
		}
	}
	return grpc.w
}

func (grpc *GRPC) Metadata() metadata.MD {
	md, ok := metadata.FromIncomingContext(grpc.w.ctx)
	if !ok {
		return metadata.New(map[string]string{})
	}
	return md
}

func (grpc *GRPC) Out() context.Context {
	md := metadata.New(map[string]string{})
	for _, key := range ContextKeys {
		if value, ok := grpc.w.ctx.Value(key).(string); ok {
			md.Set(string(key), value)
		}
	}
	return metadata.NewOutgoingContext(grpc.w.ctx, md)
}
