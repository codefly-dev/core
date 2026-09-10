package resources

import (
	"fmt"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// ReadinessRequirement is one predicate that must hold before a consumer may be
// considered ready. A consumer is ready only when every requirement passes.
type ReadinessRequirement struct {
	// Dependency is the module/service the requirement is about.
	Dependency string
	// Endpoint is the probed endpoint name, empty for an endpointless dependency.
	Endpoint string
	// API is the probed endpoint's API, empty for an endpointless dependency.
	API string
	// Probe is the predicate to evaluate, with defaults already applied.
	Probe *basev0.Probe
	// Secured is the endpoint's declared transport security. It travels with the
	// requirement because it is recorded in the endpoint's API details, which the
	// requirement does not carry: an evaluator that had to look it up separately
	// would default a TLS endpoint to plaintext and report it unreachable.
	Secured bool
	// Declared is false when the producer declared no readiness and the
	// requirement fell back to legacy transport-only semantics. Consumers use it
	// to report that an open socket is all they were given to check.
	Declared bool
}

// Predicate renders what this requirement demands.
func (r *ReadinessRequirement) Predicate() string {
	predicate := DescribeProbe(r.Probe)
	if !r.Declared {
		predicate += " (legacy: endpoint declares no readiness)"
	}
	return predicate
}

// String identifies the requirement's target and its predicate.
func (r *ReadinessRequirement) String() string {
	if r.Endpoint == "" {
		return fmt.Sprintf("%s: %s", r.Dependency, r.Predicate())
	}
	return fmt.Sprintf("%s/%s: %s", r.Dependency, r.Endpoint, r.Predicate())
}

// Result stamps an evaluation of this requirement, carrying the predicate that
// was required so a failure names it instead of reporting "not ready".
func (r *ReadinessRequirement) Result(outcome basev0.ProbeOutcome, failure basev0.ProbeFailureKind, message string) *basev0.ProbeResult {
	return &basev0.ProbeResult{
		Dependency:  r.Dependency,
		Endpoint:    r.Endpoint,
		Api:         r.API,
		Intent:      basev0.ProbeIntent_PROBE_INTENT_READINESS,
		Kind:        ProbeKindOf(r.Probe),
		Outcome:     outcome,
		FailureKind: failure,
		Predicate:   r.Predicate(),
		Message:     message,
	}
}

// EndpointReadinessProbe returns the readiness predicate to evaluate against an
// endpoint, and whether the producer declared one. Absence resolves to a
// transport check — the legacy semantics — rather than to nothing, so an
// undeclared endpoint is still checked and still reported as unverified health.
func EndpointReadinessProbe(endpoint *basev0.Endpoint) (*basev0.Probe, bool) {
	if probe := endpoint.GetHealth().GetReadiness(); probe.GetPredicate() != nil {
		return probe, true
	}
	return &basev0.Probe{Predicate: &basev0.Probe_Transport{Transport: &basev0.TransportProbe{}}}, false
}

// EndpointProbePlan is the producing side of the contract: the probes a runtime
// or a deployment renderer emits for one endpoint. Liveness and startup stay
// nil when undeclared — a restart policy is not something to infer.
type EndpointProbePlan struct {
	Readiness *basev0.Probe
	Liveness  *basev0.Probe
	Startup   *basev0.Probe
	// ReadinessDeclared is false when Readiness is the legacy transport fallback.
	ReadinessDeclared bool
}

// PlanEndpointProbes resolves the probes declared for one endpoint.
func PlanEndpointProbes(endpoint *basev0.Endpoint) *EndpointProbePlan {
	readiness, declared := EndpointReadinessProbe(endpoint)
	return &EndpointProbePlan{
		Readiness:         readiness,
		Liveness:          endpoint.GetHealth().GetLiveness(),
		Startup:           endpoint.GetHealth().GetStartup(),
		ReadinessDeclared: declared,
	}
}

// endpointlessRequirement turns a typed prerequisite into the predicate that
// satisfies it when there is no endpoint to probe. PrerequisiteEndpointHealth
// with nothing to health-check falls back to the owning agent's lifecycle: that
// is what a legacy endpointless dependency has always meant.
func endpointlessRequirement(unique string, prerequisite Prerequisite) *ReadinessRequirement {
	var probe *basev0.Probe
	switch prerequisite {
	case PrerequisiteCompletion:
		probe = &basev0.Probe{Predicate: &basev0.Probe_Completion{Completion: &basev0.CompletionProbe{}}}
	default:
		probe = &basev0.Probe{Predicate: &basev0.Probe_Agent{Agent: &basev0.AgentProbe{}}}
	}
	return &ReadinessRequirement{Dependency: unique, Probe: probe, Declared: true}
}

// PlanServiceDependencyReadiness returns every predicate a consumer must
// satisfy for one dependency. A dependency that resolves to endpoints yields
// one requirement per required endpoint; one that resolves to none yields the
// single lifecycle-or-completion requirement its readiness mode selects.
func PlanServiceDependencyReadiness(dependency *ServiceDependency, endpoints []*basev0.Endpoint) ([]*ReadinessRequirement, error) {
	// The kind decides what the consumer waits for, and rejects the declarations
	// it contradicts — one-shot work asked to serve endpoints, an unknown kind.
	if err := dependency.Validate(); err != nil {
		return nil, err
	}
	resolved, err := ResolveServiceDependencyEndpoints(dependency, endpoints)
	if err != nil {
		return nil, err
	}
	// The other direction: endpoint health onto a producer that exports none is
	// a wait that never ends. Only the caller planning readiness has both sides.
	if err := ValidateDependencyPrerequisite(dependency, resolved); err != nil {
		return nil, err
	}
	if dependency.Prerequisite() == PrerequisiteNone {
		return nil, nil
	}
	var required []*basev0.Endpoint
	for _, endpoint := range resolved {
		if dependency.RequiresEndpoint(endpoint.Name, endpoint.Api) {
			required = append(required, endpoint)
		}
	}
	if len(required) == 0 {
		if len(resolved) > 0 {
			return nil, fmt.Errorf("dependency %s consumes %d endpoint(s) but requires none for readiness; drop 'required: false' from the endpoint it must wait on, or declare kind %q to not gate on it at all",
				dependency.Unique(), len(resolved), DependencyKindExternal)
		}
		return []*ReadinessRequirement{endpointlessRequirement(dependency.Unique(), dependency.Prerequisite())}, nil
	}
	requirements := make([]*ReadinessRequirement, 0, len(required))
	for _, endpoint := range required {
		probe, declared := EndpointReadinessProbe(endpoint)
		requirements = append(requirements, &ReadinessRequirement{
			Dependency: dependency.Unique(),
			Endpoint:   endpoint.Name,
			API:        endpoint.Api,
			Probe:      probe,
			Secured:    EndpointSecured(endpoint),
			Declared:   declared,
		})
	}
	return requirements, nil
}

// PlanJobDependencyReadiness returns the single predicate a consumer must
// satisfy for a depended-on job.
func PlanJobDependencyReadiness(dependency *JobDependency) (*ReadinessRequirement, error) {
	if err := dependency.Kind.Validate(); err != nil {
		return nil, fmt.Errorf("job dependency %s: %w", dependency.Unique(), err)
	}
	prerequisite := dependency.Prerequisite()
	if prerequisite == PrerequisiteNone {
		return nil, nil
	}
	return endpointlessRequirement(dependency.Unique(), prerequisite), nil
}

// PlanReadiness returns every predicate a consumer must satisfy across all of
// its service dependencies. They combine conjunctively: readiness holds only
// when all of them do.
func PlanReadiness(dependencies []*ServiceDependency, endpoints []*basev0.Endpoint) ([]*ReadinessRequirement, error) {
	var requirements []*ReadinessRequirement
	for _, dependency := range dependencies {
		planned, err := PlanServiceDependencyReadiness(dependency, endpoints)
		if err != nil {
			return nil, err
		}
		requirements = append(requirements, planned...)
	}
	return requirements, nil
}

// ValidateReadiness reports the first unsatisfiable readiness declaration
// across a consumer's dependencies: an unsupported mode, an endpoint reference
// the producer does not declare, or a mode that contradicts the endpoints it
// requires.
func ValidateReadiness(dependencies []*ServiceDependency, endpoints []*basev0.Endpoint) error {
	_, err := PlanReadiness(dependencies, endpoints)
	return err
}

// FailingPredicates names the predicates that did not hold in a report, so a
// consumer can say which check failed rather than that something did.
func FailingPredicates(report *basev0.HealthReport) []string {
	var failing []string
	for _, result := range report.GetResults() {
		if result.GetOutcome() != basev0.ProbeOutcome_PROBE_OUTCOME_FAILED {
			continue
		}
		target := result.GetDependency()
		if endpoint := result.GetEndpoint(); endpoint != "" {
			target = fmt.Sprintf("%s/%s", target, endpoint)
		}
		message := fmt.Sprintf("%s: %s", target, result.GetPredicate())
		if detail := result.GetMessage(); detail != "" {
			message = fmt.Sprintf("%s (%s)", message, detail)
		}
		failing = append(failing, message)
	}
	return failing
}
