package services

import (
	"context"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// NetworkInstanceForRestRouteGroup finds the proper network mapping for a given REST route group
func NetworkInstanceForRestRouteGroup(ctx context.Context, mappings []*basev0.NetworkMapping, group *resources.RestRouteGroup, networkAccess *basev0.NetworkAccess) (*basev0.NetworkInstance, error) {
	w := wool.Get(ctx).In("services.NetworkMappingForRoute")
	for _, m := range mappings {
		if rest := resources.IsRest(ctx, m.Endpoint); rest == nil {
			continue
		}
		if m.Endpoint.Module == group.Module && m.Endpoint.Service == group.Service {
			for _, instance := range m.Instances {
				if instance.Access.Kind == networkAccess.Kind {
					return instance, nil
				}
			}
		}
	}
	return nil, w.NewError("cannot find network mapping for route <%v>", group)
}
