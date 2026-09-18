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
// visibility does not permit the consuming service's module.
//
// The scope is the receiver's: a view restricted with ForStage judges the edges
// that constrain that stage, and an unrestricted view judges every declared
// edge — the same answer Workspace.ValidateServiceDependencies gives, for the
// same reason. A lint over a whole workspace has no stage to scope to.
func (d *ServiceDependencies) VerifyVisibility(ctx context.Context) error {
	return verifyVisibility(ctx, d.uniqueToService, d.admits)
}

// admits reports whether a dependency takes part in the stage this view was
// restricted to. An unrestricted view carries no stage and admits every edge.
func (d *ServiceDependencies) admits(dep *resources.ServiceDependency) bool {
	if d.stage == "" {
		return true
	}
	return dep.Kind.Participates(d.stage)
}

// verifyVisibility checks the declared dependencies that admits selects between
// the given services. A dependency onto a service outside the set (not part of
// the loaded graph or closure) is skipped: its endpoints are not available to
// check here.
//
// The verdict itself is resources': the same functions the static workspace
// pass and the run-time resolution call, so what this refuses is exactly what
// they refuse. Only the set of edges judged is decided here.
func verifyVisibility(ctx context.Context, services map[string]*resources.Service, admits func(*resources.ServiceDependency) bool) error {
	w := wool.Get(ctx).In("architecture.VerifyVisibility")

	var violations []string
	for _, consumer := range services {
		identity, err := consumer.Identity()
		if err != nil {
			return w.Wrapf(err, "cannot get identity for service %s", consumer.Name)
		}
		for _, dep := range consumer.ServiceDependencies {
			if !admits(dep) {
				continue
			}
			target, ok := services[dep.Unique()]
			if !ok {
				continue
			}
			endpoints, err := target.DependencyEndpoints()
			if err != nil {
				return w.Wrapf(err, "cannot read endpoints of service %s", dep.Unique())
			}
			if err := resources.ValidateServiceDependencyEndpoints(dep, endpoints); err != nil {
				violations = append(violations, fmt.Sprintf(
					"%s depends on %s: %s", identity.Unique(), dep.Unique(), err))
				continue
			}
			if _, err := resources.ConsumedDependencyEndpoints(identity.Module, dep, endpoints); err != nil {
				violations = append(violations, fmt.Sprintf(
					"%s depends on %s: %s", identity.Unique(), dep.Unique(), err))
			}
		}
	}
	if len(violations) == 0 {
		return nil
	}
	sort.Strings(violations)
	return w.NewError("visibility violations:\n  %s", strings.Join(violations, "\n  "))
}
