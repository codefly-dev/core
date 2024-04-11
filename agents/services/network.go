package services

import (
	"context"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/wool"
)

// NetworInstanceForRestRouteGroup finds the proper network mapping for a given REST route group
func NetworkInstanceForRestRouteGroup(ctx context.Context, group *configurations.RestRouteGroup, scope basev0.NetworkScope, mappings []*basev0.NetworkMapping) (*basev0.NetworkInstance, error) {
	w := wool.Get(ctx).In("services.NetworkMappingForRoute")
	for _, m := range mappings {
		if rest := configurations.IsRest(ctx, m.Endpoint); rest == nil {
			continue
		}
		if m.Endpoint.Application == group.Application && m.Endpoint.Service == group.Service {
			for _, instance := range m.Instances {
				if instance.Scope == scope {
					return instance, nil
				}
			}
		}
	}
	return nil, w.NewError("cannot find network mapping for route <%v>", group)
}
