package services

import (
	"context"

	"github.com/codefly-dev/core/configurations"
	runtimev0 "github.com/codefly-dev/core/generated/go/services/runtime/v0"
	"github.com/codefly-dev/core/wool"
)

// NetworkMappingForRoute finds the proper network mapping for a given route
func NetworkMappingForRoute(ctx context.Context, route *configurations.RestRoute, mappings []*runtimev0.NetworkMapping) (*runtimev0.NetworkMapping, error) {
	w := wool.Get(ctx).In("services.NetworkMappingForRoute")
	for _, m := range mappings {
		if rest := m.Endpoint.Api.GetRest(); rest != nil {
			for _, r := range rest.Routes {
				if r.Path == route.Path {
					return m, nil
				}
			}
			if m.Application == route.Application && m.Service == route.Service {
				return m, nil
			}
		}
	}
	return nil, w.NewError("cannot find network mapping for route <%s>", route)
}
