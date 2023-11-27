package services

import (
	"context"

	"github.com/codefly-dev/core/configurations"
	runtimev1 "github.com/codefly-dev/core/proto/v1/go/services/runtime"
)

// NetworkMappingForRoute finds the proper network mapping for a given route
func NetworkMappingForRoute(ctx context.Context, route *configurations.RestRoute, mappings []*runtimev1.NetworkMapping) (*runtimev1.NetworkMapping, error) {
	logger := AgentLogger(ctx)
	for _, m := range mappings {
		if rest := m.Endpoint.Api.GetRest(); rest != nil {
			for _, r := range rest.Routes {
				if r.Path == route.Path {
					logger.TODO("METHODS AS WELL")
					return m, nil
				}
			}
			if m.Application == route.Application && m.Service == route.Service {
				return m, nil
			}
		}
	}
	return nil, logger.Errorf("cannot find network mapping for route <%s>", route)
}
