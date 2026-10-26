package architecture

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// WithRuntimePrerequisites adds mandatory runtime ordering to
// NewServiceDependencies: each consumer starts after its named prerequisites.
// Keys and values are canonical module/service identities of loaded services.
// The option grants no endpoint access, configuration or readiness declaration.
// A real excluded or skipped consumer imposes no requirement; an active consumer
// naming a missing or excluded prerequisite is refused, as is a runtime cycle.
// Input is copied; repeated options replace the previous map. SelectClosure
// refuses nonempty use because its manifest-only walk cannot honor these edges.
func WithRuntimePrerequisites(requiredByConsumer map[string][]string) DependencyOption {
	snapshot := cloneRuntimePrerequisites(requiredByConsumer)
	return func(opt *DependencyOptions) error {
		opt.RuntimePrerequisites = cloneRuntimePrerequisites(snapshot)
		return nil
	}
}

func cloneRuntimePrerequisites(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for consumer, prerequisites := range in {
		out[consumer] = slices.Clone(prerequisites)
	}
	return out
}

func (d *ServiceDependencies) addRuntimePrerequisiteEdges(ctx context.Context, graph *DAG, loaded map[string]bool) error {
	for _, consumer := range slices.Sorted(maps.Keys(d.options.RuntimePrerequisites)) {
		if !canonicalServiceUnique(consumer) || !loaded[consumer] {
			return fmt.Errorf("runtime prerequisite consumer %q is not a loaded module/service", consumer)
		}
		if d.options.ExcludeService[consumer] || d.options.SkipDependencyFor[consumer] {
			continue
		}
		for _, prerequisite := range d.options.RuntimePrerequisites[consumer] {
			if !canonicalServiceUnique(prerequisite) || !loaded[prerequisite] {
				return fmt.Errorf("runtime prerequisite %q of %q is not a loaded module/service", prerequisite, consumer)
			}
			if prerequisite == consumer || d.options.ExcludeService[prerequisite] {
				return fmt.Errorf("runtime prerequisite %q of %q is self-referential or excluded", prerequisite, consumer)
			}
			graph.AddKindedEdge(prerequisite, consumer, resources.DependencyKindRuntime)
			if slices.Contains(graph.EdgeKinds(prerequisite, consumer), resources.DependencyKindExternal) {
				wool.Get(ctx).Info("a runtime prerequisite orders a producer its consumer declares external",
					wool.Field("consumer", consumer), wool.Field("producer", prerequisite))
				graph.dropEdgeKind(prerequisite, consumer, resources.DependencyKindExternal)
			}
		}
	}
	return nil
}

func canonicalServiceUnique(unique string) bool {
	module, service, ok := strings.Cut(unique, "/")
	return ok && module != "" && service != "" && !strings.Contains(service, "/") && strings.TrimSpace(unique) == unique
}
