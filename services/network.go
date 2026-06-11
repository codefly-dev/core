package services

import (
	"context"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	resources "github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// NetworkMappingForRestRouteGroup finds the proper network mapping for a given route group
func NetworkMappingForRestRouteGroup(ctx context.Context, group *resources.RestRouteGroup, mappings []*basev0.NetworkMapping) (*basev0.NetworkMapping, error) {
	w := wool.Get(ctx).In("services.NetworkMappingForRoute")
	for _, m := range mappings {
		if rest := resources.IsRest(ctx, m.Endpoint); rest != nil {
			// Match BOTH module AND service. With ||, the first REST mapping in
			// the same module (any service) won, resolving route groups to the
			// wrong service's upstream in multi-service modules. The twin in
			// agents/services/network.go already uses &&.
			if m.Endpoint.Module == group.Module && m.Endpoint.Service == group.Service {
				return m, nil
			}
		}
	}
	return nil, w.NewError("cannot find network mapping for route <%v>", group)
}
