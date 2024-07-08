package wool

import (
	"context"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func GRPCInstrumentation() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	}
}

func InitTracer() (func(context.Context) error, error) {
	secureOption := otlptracegrpc.WithInsecure()
	exporter, err := otlptrace.New(context.Background(), otlptracegrpc.NewClient(secureOption, otlptracegrpc.WithEndpoint("localhost:4317")))

	if err != nil {
		return nil, err
	}
	resources, err := resource.New(context.Background(), resource.WithAttributes(attribute.String("service.name", "SCRET"), attribute.String("library.language", "go")))
	if err != nil {
		return nil, err
	}

	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()), sdktrace.WithBatcher(exporter), sdktrace.WithResource(resources)))
	return exporter.Shutdown, nil
}
