package services

import (
	"context"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/wool"
)

// NetworkMappingForRestRouteGroup finds the proper network mapping for a given REST route group
func NetworkMappingForRestRouteGroup(ctx context.Context, group *configurations.RestRouteGroup, mappings []*basev0.NetworkMapping) (*basev0.NetworkMapping, error) {
	w := wool.Get(ctx).In("services.NetworkMappingForRoute")
	for _, m := range mappings {
		if rest := configurations.IsRest(ctx, m.Endpoint); rest != nil {
			if m.Endpoint.Application == group.Application || m.Endpoint.Service == group.Service {
				return m, nil
			}
		}
	}
	return nil, w.NewError("cannot find network mapping for route <%v>", group)
}

// NetworkMappingForGRPCRoute finds the proper network mapping for a given gRPC route
func NetworkMappingForGRPCRoute(ctx context.Context, route *configurations.GRPCRoute, mappings []*basev0.NetworkMapping) (*basev0.NetworkMapping, error) {
	w := wool.Get(ctx).In("services.NetworkMappingForRoute")
	for _, m := range mappings {
		if grpc := configurations.IsGRPC(ctx, m.Endpoint); grpc != nil {
			if m.Endpoint.Application == route.Application || m.Endpoint.Service == route.Service {
				return m, nil
			}
		}
	}
	return nil, w.NewError("cannot find network mapping for route <%v>", route)
}
