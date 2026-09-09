package resources

import (
	"fmt"
	"slices"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
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

// Stage is an ELEMENTARY execution graph — one that can be topologically
// sorted on its own. There are exactly two: building an artifact, and running a
// process. Every edge constrains one, the other, both, or neither.
type Stage string

const (
	StageBuild Stage = "build"
	StageRun   Stage = "run"
)

// Stages returns every elementary stage, in the order they are executed.
func Stages() []Stage {
	return []Stage{StageBuild, StageRun}
}

// Validate reports whether the stage is one codefly knows about.
func (s Stage) Validate() error {
	if slices.Contains(Stages(), s) {
		return nil
	}
	return fmt.Errorf("unknown stage %q: expected one of %s", string(s), stageNames())
}

// Phase is an operation a caller asks for. It is NOT a graph: `test` and
// `deploy` each build and then run, so they decompose into ordered stages.
//
// Merging their stages into one graph would be wrong, not merely imprecise: an
// API that consumes a database at runtime while the database bootstrap consumes
// the API contract at build time has an edge in each direction, and the union of
// the two is a cycle. Sorting the stages separately is what keeps that
// legitimate arrangement sortable while a real same-stage cycle still fails.
type Phase string

const (
	PhaseBuild  Phase = "build"
	PhaseRun    Phase = "run"
	PhaseTest   Phase = "test"
	PhaseDeploy Phase = "deploy"
)

// Phases returns every operation a caller can ask for.
func Phases() []Phase {
	return []Phase{PhaseBuild, PhaseRun, PhaseTest, PhaseDeploy}
}

var phaseStages = map[Phase][]Stage{
	PhaseBuild:  {StageBuild},
	PhaseRun:    {StageRun},
	PhaseTest:   {StageBuild, StageRun},
	PhaseDeploy: {StageBuild, StageRun},
}

// Stages returns the elementary stages this phase decomposes into, in
// execution order. Testing or deploying a service builds it first, so a build
// input constrains those operations even though it constrains no run.
func (p Phase) Stages() []Stage {
	return slices.Clone(phaseStages[p])
}

// Validate reports whether the phase is one codefly knows about.
func (p Phase) Validate() error {
	if slices.Contains(Phases(), p) {
		return nil
	}
	return fmt.Errorf("unknown phase %q: expected one of %s", string(p), phaseNames())
}

// ParsePhase converts a phase name to a Phase.
func ParsePhase(s string) (Phase, error) {
	phase := Phase(s)
	if err := phase.Validate(); err != nil {
		return "", err
	}
	return phase, nil
}

func phaseNames() string {
	names := make([]string, 0, len(Phases()))
	for _, phase := range Phases() {
		names = append(names, string(phase))
	}
	return strings.Join(names, ", ")
}

func stageNames() string {
	names := make([]string, 0, len(Stages()))
	for _, stage := range Stages() {
		names = append(names, string(stage))
	}
	return strings.Join(names, ", ")
}

var dependencyKindStages = map[DependencyKind][]Stage{
	DependencyKindLegacy:     {StageBuild, StageRun},
	DependencyKindBuild:      {StageBuild},
	DependencyKindSchema:     {StageBuild},
	DependencyKindRuntime:    {StageRun},
	DependencyKindCompletion: {StageRun},
	DependencyKindExternal:   nil,
}

// DeclarableDependencyKinds returns the kinds that can be written in YAML,
// omitting the legacy (absent) kind. Callers use it to enumerate the vocabulary
// exhaustively — a mapping keyed on dependency kinds is only complete if it
// covers every entry here.
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
	if _, ok := dependencyKindStages[k]; !ok {
		names := make([]string, 0, len(DeclarableDependencyKinds()))
		for _, kind := range DeclarableDependencyKinds() {
			names = append(names, string(kind))
		}
		return fmt.Errorf("unknown dependency kind %q: expected one of %s", string(k), strings.Join(names, ", "))
	}
	return nil
}

// Stages returns the elementary stages this kind constrains. The result is a
// copy: the table it comes from is package state shared by every caller.
func (k DependencyKind) Stages() []Stage {
	return slices.Clone(dependencyKindStages[k])
}

// Participates reports whether an edge of this kind constrains the given stage.
func (k DependencyKind) Participates(stage Stage) bool {
	return slices.Contains(dependencyKindStages[k], stage)
}

// ConstrainsPhase reports whether an edge of this kind constrains any stage of
// the given phase.
func (k DependencyKind) ConstrainsPhase(phase Phase) bool {
	for _, stage := range phase.Stages() {
		if k.Participates(stage) {
			return true
		}
	}
	return false
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

// ValidateDependencyPrerequisite rejects a dependency whose prerequisite the
// producer cannot ever satisfy. A dependency that waits for endpoint health
// onto a producer that exports no endpoint waits forever: there is nothing to
// health-check. This is the counterpart to ServiceDependency.Validate, which
// rejects the other direction (one-shot work asked to serve endpoints); it
// lives here because only the workspace knows the producer's endpoints.
//
// Legacy dependencies are exempt. Endpointless producers under an undeclared
// kind are how existing workspaces express one-shot work, and failing them
// would reject configurations that work today.
func ValidateDependencyPrerequisite(dependency *ServiceDependency, producerEndpoints []*basev0.Endpoint) error {
	if dependency.Kind != DependencyKindRuntime {
		return nil
	}
	if len(producerEndpoints) > 0 {
		return nil
	}
	return fmt.Errorf(
		"service dependency %s of kind %q waits for endpoint health but %s exports no endpoint; use kind %q for one-shot work",
		dependency.Unique(), DependencyKindRuntime, dependency.Unique(), DependencyKindCompletion)
}

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
