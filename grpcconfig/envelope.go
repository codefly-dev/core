// Package grpcconfig defines the bounded transport contract shared by
// Codefly-owned gRPC clients and servers.
package grpcconfig

import "google.golang.org/grpc"

// MaxTypedMessageBytes is the largest encoded typed request or response that
// Codefly transports through one unary gRPC call. Complete semantic indexes
// from real frameworks exceed gRPC's 4 MiB default; 64 MiB keeps that current
// contract usable while retaining a finite memory and abuse boundary.
const MaxTypedMessageBytes = 64 << 20

// TypedMessageServerOptions applies the shared bounded envelope to a gRPC
// server. Callers append these options to their service-specific interceptors,
// credentials, and telemetry.
func TypedMessageServerOptions() []grpc.ServerOption {
	return typedMessageServerOptions(MaxTypedMessageBytes)
}

// TypedMessageClientDialOption applies the same envelope to every call made on
// a client connection.
func TypedMessageClientDialOption() grpc.DialOption {
	return typedMessageClientDialOption(MaxTypedMessageBytes)
}

func typedMessageServerOptions(maxBytes int) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.MaxRecvMsgSize(maxBytes),
		grpc.MaxSendMsgSize(maxBytes),
	}
}

func typedMessageClientDialOption(maxBytes int) grpc.DialOption {
	return grpc.WithDefaultCallOptions(
		grpc.MaxCallRecvMsgSize(maxBytes),
		grpc.MaxCallSendMsgSize(maxBytes),
	)
}
