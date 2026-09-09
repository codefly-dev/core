package resources

import (
	"fmt"
	"slices"
	"strings"
)

// DependencyKind classifies WHY a service depends on another one. The kind
// decides which execution phases the edge participates in: a codegen input is
// irrelevant to `run`, and a consumed endpoint is irrelevant to `build`.
// Collapsing both into one untyped edge is what makes a legitimate two-phase
// relationship — an API consuming a database at runtime while the database
// bootstrap consumes the API schema at build time — look like a cycle.
type DependencyKind string

const (
	// DependencyKindLegacy is the kind of a dependency that declares none. It
	// participates in every phase, which is exactly what an untyped edge did
	// before kinds existed, so existing workspaces keep their order.
	DependencyKindLegacy DependencyKind = ""

	// DependencyKindBuild is a build/codegen input: the producer must be built
	// before the consumer can be built. It never needs to run.
	DependencyKindBuild DependencyKind = "build"

	// DependencyKindRuntime is a consumed endpoint: the producer must be
	// running and healthy before the consumer starts.
	DependencyKindRuntime DependencyKind = "runtime"

	// DependencyKindCompletion is a one-shot prerequisite: the producer's work
	// (a migration, a seed job) must COMPLETE — not merely be reachable —
	// before the consumer starts.
	DependencyKindCompletion DependencyKind = "completion"

	// DependencyKindSchema is a schema artifact contribution: the consumer
	// reads the producer's declared API contract at build time. The contract is
	// the producer's declared endpoints, so a schema edge is validated against
	// them like any other.
	DependencyKindSchema DependencyKind = "schema"

	// DependencyKindExternal is a capability provided outside the workspace.
	// It is declared so configuration and documentation can name it; codefly
	// never builds, starts or orders it.
	DependencyKindExternal DependencyKind = "external"
)

// Phase is an execution phase whose closure is computed from the dependency
// graph. Callers select the phase they are executing rather than passing a
// bag of booleans down the call chain.
type Phase string

const (
	PhaseBuild  Phase = "build"
	PhaseRun    Phase = "run"
	PhaseTest   Phase = "test"
	PhaseDeploy Phase = "deploy"
)

// Phases returns every execution phase, in the order they are typically run.
func Phases() []Phase {
	return []Phase{PhaseBuild, PhaseRun, PhaseTest, PhaseDeploy}
}

// ParsePhase converts a phase name to a Phase.
func ParsePhase(s string) (Phase, error) {
	phase := Phase(s)
	if slices.Contains(Phases(), phase) {
		return phase, nil
	}
	return "", fmt.Errorf("unknown phase %q: expected one of %s", s, phaseNames())
}

func phaseNames() string {
	names := make([]string, 0, len(Phases()))
	for _, phase := range Phases() {
		names = append(names, string(phase))
	}
	return strings.Join(names, ", ")
}

var dependencyKindPhases = map[DependencyKind][]Phase{
	DependencyKindLegacy:     {PhaseBuild, PhaseRun, PhaseTest, PhaseDeploy},
	DependencyKindBuild:      {PhaseBuild},
	DependencyKindSchema:     {PhaseBuild},
	DependencyKindRuntime:    {PhaseRun, PhaseTest, PhaseDeploy},
	DependencyKindCompletion: {PhaseRun, PhaseTest, PhaseDeploy},
	DependencyKindExternal:   nil,
}

// DeclarableDependencyKinds returns the kinds that can be written in YAML,
// omitting the legacy (absent) kind.
func DeclarableDependencyKinds() []DependencyKind {
	return []DependencyKind{
		DependencyKindBuild,
		DependencyKindCompletion,
		DependencyKindExternal,
		DependencyKindRuntime,
		DependencyKindSchema,
	}
}

// Validate reports whether the kind is one codefly knows about.
func (k DependencyKind) Validate() error {
	if _, ok := dependencyKindPhases[k]; !ok {
		names := make([]string, 0, len(DeclarableDependencyKinds()))
		for _, kind := range DeclarableDependencyKinds() {
			names = append(names, string(kind))
		}
		return fmt.Errorf("unknown dependency kind %q: expected one of %s", string(k), strings.Join(names, ", "))
	}
	return nil
}

// Phases returns the execution phases this kind participates in.
func (k DependencyKind) Phases() []Phase {
	return dependencyKindPhases[k]
}

// Participates reports whether an edge of this kind constrains the given phase.
func (k DependencyKind) Participates(phase Phase) bool {
	return slices.Contains(dependencyKindPhases[k], phase)
}

// Prerequisite is what a consumer must observe about a producer before it may
// start. It is the typed hand-off to endpoint readiness predicates: an
// endpoint-health prerequisite is satisfied by a healthy consumed endpoint, a
// completion prerequisite by a finished one-shot run. The two are not
// interchangeable — polling a migration job for endpoint health never succeeds,
// and treating a long-running service as "done" starts its consumers early.
type Prerequisite string

const (
	PrerequisiteNone           Prerequisite = "none"
	PrerequisiteEndpointHealth Prerequisite = "endpoint-health"
	PrerequisiteCompletion     Prerequisite = "completion"
)

// Prerequisite returns what a consumer waits for before starting.
func (k DependencyKind) Prerequisite() Prerequisite {
	switch k {
	case DependencyKindLegacy, DependencyKindRuntime:
		return PrerequisiteEndpointHealth
	case DependencyKindCompletion:
		return PrerequisiteCompletion
	default:
		return PrerequisiteNone
	}
}
