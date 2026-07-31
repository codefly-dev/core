package architecture

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// VerifyVisibility fails closed when a service depends on an endpoint whose
// visibility does not permit the consuming service's module. It is the single
// source of truth the CLI `verify` phase enforces so that a declared dependency
// and the NetworkPolicy generated from the same topology can never diverge.
//
// A dependency onto a service outside the workspace (not part of the loaded
// graph) is skipped: its endpoints are not available to check here.
func (d *ServiceDependencies) VerifyVisibility(ctx context.Context) error {
	w := wool.Get(ctx).In("architecture.VerifyVisibility")

	var violations []string
	for _, consumer := range d.uniqueToService {
		identity, err := consumer.Identity()
		if err != nil {
			return w.Wrapf(err, "cannot get identity for service %s", consumer.Name)
		}
		for _, dep := range consumer.ServiceDependencies {
			target, ok := d.uniqueToService[dep.Unique()]
			if !ok {
				continue
			}
			for _, ep := range requestedEndpoints(dep, target) {
				if !ep.AllowsModule(identity.Module) {
					violations = append(violations, fmt.Sprintf(
						"%s depends on %s/%s (visibility %q) but module %q is not permitted",
						identity.Unique(), ep.ServiceUnique(), ep.Name, ep.Visibility, identity.Module))
				}
			}
		}
	}
	if len(violations) == 0 {
		return nil
	}
	sort.Strings(violations)
	return w.NewError("visibility violations:\n  %s", strings.Join(violations, "\n  "))
}

// requestedEndpoints returns the target endpoints a dependency consumes: the
// explicitly listed ones, or all of the target's endpoints when the dependency
// lists none (it then receives everything the target exposes).
func requestedEndpoints(dep *resources.ServiceDependency, target *resources.Service) []*resources.Endpoint {
	if len(dep.Endpoints) == 0 {
		return target.Endpoints
	}
	byName := make(map[string]*resources.Endpoint, len(target.Endpoints))
	for _, ep := range target.Endpoints {
		byName[ep.Name] = ep
	}
	var out []*resources.Endpoint
	for _, ref := range dep.Endpoints {
		if ep, ok := byName[ref.Name]; ok {
			out = append(out, ep)
		}
	}
	return out
}
