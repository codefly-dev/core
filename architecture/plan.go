package architecture

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/executionplan"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// PlanOptions carries what the caller decides rather than what the workspace
// declares.
type PlanOptions struct {
	Phase       executionplan.Phase
	Environment string
	StatePolicy executionplan.StatePolicy
	Invocation  *executionplan.Invocation
}

// Plan turns the resolved closure into an immutable execution plan. It verifies
// the closure first, so an unresolved node, a cycle or a visibility violation is
// reported as such rather than as a malformed plan.
func (closure *Closure) Plan(ctx context.Context, options PlanOptions) (*executionplan.Plan, error) {
	w := wool.Get(ctx).In("architecture.Closure.Plan", wool.NameField(closure.Target))
	if err := closure.Verify(ctx); err != nil {
		return nil, w.Wrap(err)
	}
	consumption, err := closure.resolveConsumption()
	if err != nil {
		return nil, w.Wrap(err)
	}
	plan := &executionplan.Plan{
		Schema: executionplan.SchemaV1,
		Requested: executionplan.Target{
			Workspace:   closure.Workspace.Name,
			Service:     closure.Target,
			Phase:       options.Phase,
			Environment: options.Environment,
		},
		Nodes:          closure.planNodes(consumption),
		Edges:          closure.planEdges(),
		Configurations: closure.planConfigurations(consumption),
		StatePolicy:    options.StatePolicy,
		Invocation:     options.Invocation,
	}
	steps, err := closure.planSchemaSteps(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	plan.SchemaSteps = steps
	canonical := plan.Canonical()
	if err := canonical.Validate(); err != nil {
		return nil, w.Wrap(err)
	}
	return canonical, nil
}

// consumption is which endpoints of a producer each selected consumer pulls in.
// It is resolved once, before the plan is assembled, so the endpoint
// requirements and the configuration origins cannot disagree about it.
type consumption map[string][]*resources.Endpoint

func consumptionKey(consumer string, producer string) string {
	return consumer + "\x00" + producer
}

func (closure *Closure) resolveConsumption() (consumption, error) {
	resolved := make(consumption)
	for consumer, service := range closure.services {
		for _, dependency := range service.ServiceDependencies {
			producer := dependency.Unique()
			target, selected := closure.services[producer]
			if !selected {
				continue
			}
			names := make([]string, 0, len(dependency.Endpoints))
			for _, reference := range dependency.Endpoints {
				names = append(names, reference.Name)
			}
			endpoints, err := target.ConsumedEndpoints(names)
			if err != nil {
				return nil, fmt.Errorf("%s depends on %s: %w", consumer, producer, err)
			}
			resolved[consumptionKey(consumer, producer)] = endpoints
		}
	}
	return resolved, nil
}

func (closure *Closure) planNodes(consumed consumption) []executionplan.Node {
	required := closure.endpointRequirements(consumed)
	nodes := make([]executionplan.Node, 0, len(closure.services))
	for unique, service := range closure.services {
		module, name := resources.SplitUnique(unique)
		node := executionplan.Node{
			ID:         unique,
			Kind:       executionplan.NodeService,
			Module:     module,
			Name:       name,
			Resolution: executionplan.Resolved,
			Endpoints:  required[unique],
			Selection:  closure.selection(unique),
		}
		if service.Agent != nil {
			node.Backend = &executionplan.Backend{
				Kind:      string(service.Agent.Kind),
				Publisher: service.Agent.Publisher,
				Name:      service.Agent.Name,
				Version:   service.Agent.Version,
			}
		}
		if artifact := closure.moduleArtifact(module); artifact != nil {
			node.Artifacts = []executionplan.Artifact{*artifact}
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func (closure *Closure) selection(unique string) executionplan.Selection {
	if unique == closure.Target {
		return executionplan.Selection{Reason: executionplan.ReasonRequestedTarget}
	}
	via := closure.via[unique]
	reason := executionplan.ReasonTransitiveDependency
	if len(via) <= 1 {
		reason = executionplan.ReasonDeclaredDependency
	}
	return executionplan.Selection{
		Reason: reason,
		Via:    via,
		Detail: fmt.Sprintf("declared as a service dependency of %s", via[len(via)-1]),
	}
}

// moduleArtifact records the winning module resolution and what it beat. The
// reference is a workspace or repository identity, never the resolved directory:
// the same workspace checked out twice must plan identically.
func (closure *Closure) moduleArtifact(module string) *executionplan.Artifact {
	resolution, ok := closure.resolutions[module]
	if !ok {
		return nil
	}
	reference := closure.references[module]
	identity := resolution.Source
	if identity == "" {
		identity = fmt.Sprintf("%s/%s", closure.Workspace.Name, module)
	}
	// Only a pinned resolution carries the version; for every other kind the
	// composed version is still the artifact's declared identity, and
	// Verification says whether the bytes were checked against it.
	version := resolution.Version
	if version == "" {
		version = reference.Version
	}
	artifact := &executionplan.Artifact{
		Reference:    identity,
		Version:      version,
		Verification: executionplan.VerificationLocal,
	}
	// The committed configuration alone yields a pinned resolution when the
	// reference carries a source, and a local checkout otherwise. Any other
	// outcome means the machine-local overlay decided.
	committed := executionplan.ReasonWorkspaceLayout
	if reference.Source != "" {
		committed = executionplan.ReasonCommittedPin
	}
	switch {
	case resolution.Kind == resources.ResolutionWorktree:
		artifact.Version = resolution.Ref
		artifact.Selection = executionplan.Selection{
			Reason: executionplan.ReasonLocalOverlay,
			Over:   []string{string(committed)},
			Detail: fmt.Sprintf("%s resolves module <%s> to a local worktree of %s at %s",
				resources.LocalOverlayConfigurationName, module, resolution.Source, resolution.Ref),
		}
	case resolution.Kind == resources.ResolutionPinned && resolution.Unverified:
		artifact.Verification = executionplan.VerificationUnverified
		artifact.Selection = executionplan.Selection{
			Reason: executionplan.ReasonLocalOverlay,
			Over:   []string{string(committed)},
			Detail: fmt.Sprintf("%s waives artifact verification for module <%s>",
				resources.LocalOverlayConfigurationName, module),
		}
	case resolution.Kind == resources.ResolutionPinned:
		artifact.Verification = executionplan.VerificationVerified
		artifact.Selection = executionplan.Selection{
			Reason: executionplan.ReasonCommittedPin,
			Detail: fmt.Sprintf("module <%s> is composed from %s at version %q", module, resolution.Source, resolution.Version),
		}
	case reference.PathOverride != nil:
		artifact.Selection = executionplan.Selection{
			Reason: executionplan.ReasonWorkspaceLayout,
			Detail: fmt.Sprintf("workspace <%s> composes module <%s> at a committed path override",
				closure.Workspace.Name, module),
		}
		if reference.Source != "" {
			artifact.Selection.Over = []string{string(executionplan.ReasonCommittedPin)}
		}
	case reference.Source != "":
		artifact.Selection = executionplan.Selection{
			Reason: executionplan.ReasonLocalOverlay,
			Over:   []string{string(executionplan.ReasonCommittedPin)},
			Detail: fmt.Sprintf("%s resolves module <%s> to a local checkout instead of its composed source",
				resources.LocalOverlayConfigurationName, module),
		}
	default:
		artifact.Selection = executionplan.Selection{
			Reason: executionplan.ReasonWorkspaceLayout,
			Detail: fmt.Sprintf("module <%s> is a local checkout of workspace <%s>", module, closure.Workspace.Name),
		}
	}
	return artifact
}

func (closure *Closure) planEdges() []executionplan.Edge {
	var edges []executionplan.Edge
	for consumer, service := range closure.services {
		for _, dependency := range service.ServiceDependencies {
			producer := dependency.Unique()
			if _, selected := closure.services[producer]; !selected {
				continue
			}
			names := make([]string, 0, len(dependency.Endpoints))
			for _, reference := range dependency.Endpoints {
				names = append(names, reference.Name)
			}
			edges = append(edges, executionplan.Edge{
				From:      producer,
				To:        consumer,
				Kind:      executionplan.KindDeclared,
				Endpoints: names,
				Selection: executionplan.Selection{
					Reason: executionplan.ReasonDeclaredDependency,
					Detail: fmt.Sprintf("%s declares %s in service-dependencies", consumer, producer),
				},
			})
		}
	}
	return edges
}

// endpointRequirements is what each selected node must expose for this closure:
// the endpoints its selected consumers reference. A node no selected consumer
// constrains — the requested target — must expose everything it declares.
func (closure *Closure) endpointRequirements(consumed consumption) map[string][]executionplan.EndpointRequirement {
	constrained := make(map[string]map[string][]string)
	for consumer, service := range closure.services {
		for _, dependency := range service.ServiceDependencies {
			producer := dependency.Unique()
			if _, selected := closure.services[producer]; !selected {
				continue
			}
			if _, exists := constrained[producer]; !exists {
				constrained[producer] = make(map[string][]string)
			}
			for _, endpoint := range consumed[consumptionKey(consumer, producer)] {
				constrained[producer][endpoint.Name] = append(constrained[producer][endpoint.Name], consumer)
			}
		}
	}
	out := make(map[string][]executionplan.EndpointRequirement, len(closure.services))
	for unique, service := range closure.services {
		for _, endpoint := range service.Endpoints {
			consumers, required := constrained[unique][endpoint.Name]
			if !required && len(constrained[unique]) > 0 {
				continue
			}
			out[unique] = append(out[unique], executionplan.EndpointRequirement{
				Name:       endpoint.Name,
				API:        endpoint.API,
				Visibility: endpoint.Visibility,
				RequiredBy: consumers,
			})
		}
	}
	return out
}

// planConfigurations records where each configuration key a consumer receives
// comes from. Values never appear: the plan carries the coordinate, and the
// runtime resolves it.
func (closure *Closure) planConfigurations(consumed consumption) []executionplan.ConfigurationOrigin {
	var origins []executionplan.ConfigurationOrigin
	for consumer, service := range closure.services {
		for _, dependency := range service.ServiceDependencies {
			producer := dependency.Unique()
			if _, selected := closure.services[producer]; !selected {
				continue
			}
			module, name := resources.SplitUnique(producer)
			for _, endpoint := range consumed[consumptionKey(consumer, producer)] {
				origins = append(origins, executionplan.ConfigurationOrigin{
					Consumer: consumer,
					Key: resources.EndpointAsEnvironmentVariableKey(&resources.EndpointInformation{
						Module:  module,
						Service: name,
						Name:    endpoint.Name,
						API:     endpoint.API,
					}),
					Origin:   executionplan.OriginServiceEndpoint,
					Producer: producer,
					Selection: executionplan.Selection{
						Reason: executionplan.ReasonDeclaredDependency,
						Detail: fmt.Sprintf("%s consumes endpoint %s of %s", consumer, endpoint.Name, producer),
					},
				})
			}
		}
		for _, dependency := range service.WorkspaceConfigurationDependencies {
			origins = append(origins, executionplan.ConfigurationOrigin{
				Consumer: consumer,
				Key:      fmt.Sprintf("%s__%s", resources.WorkspaceConfigurationPrefix, resources.NameToKey(dependency)),
				Origin:   executionplan.OriginWorkspaceConfiguration,
				Selection: executionplan.Selection{
					Reason: executionplan.ReasonDeclaredDependency,
					Detail: fmt.Sprintf("%s declares workspace configuration %q", consumer, dependency),
				},
			})
		}
	}
	return origins
}

// planSchemaSteps collects the jobs the closure's modules declare that operate
// on a selected service. Their order is the declaration order — a migration
// sequence is not a set — so canonicalization leaves it alone.
func (closure *Closure) planSchemaSteps(ctx context.Context) ([]executionplan.SchemaStep, error) {
	w := wool.Get(ctx).In("architecture.Closure.planSchemaSteps")
	var steps []executionplan.SchemaStep
	for _, reference := range closure.Workspace.Modules {
		module, loaded := closure.modules[reference.Name]
		if !loaded {
			continue
		}
		for _, jobReference := range module.JobReferences {
			job, err := module.LoadJobFromReference(ctx, jobReference)
			if err != nil {
				return nil, w.Wrapf(err, "cannot load job <%s> of module <%s>", jobReference.Name, module.Name)
			}
			var targets []string
			for _, dependency := range job.ServiceDependencies {
				if _, selected := closure.services[dependency.Unique()]; selected {
					targets = append(targets, dependency.Unique())
				}
			}
			if len(targets) == 0 {
				continue
			}
			step := executionplan.SchemaStep{
				ID:      fmt.Sprintf("%s/%s", module.Name, job.Name),
				Module:  module.Name,
				Name:    job.Name,
				Targets: targets,
				Selection: executionplan.Selection{
					Reason: executionplan.ReasonSchemaPrerequisite,
					Detail: fmt.Sprintf("job <%s> of module <%s> operates on the selected closure", job.Name, module.Name),
				},
			}
			if job.Agent != nil {
				step.Backend = &executionplan.Backend{
					Kind:      string(job.Agent.Kind),
					Publisher: job.Agent.Publisher,
					Name:      job.Agent.Name,
					Version:   job.Agent.Version,
				}
			}
			steps = append(steps, step)
		}
	}
	return steps, nil
}
