package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
	"github.com/stretchr/testify/require"
)

func TestDependencyKindStages(t *testing.T) {
	cases := []struct {
		kind   resources.DependencyKind
		stages []resources.Stage
	}{
		{resources.DependencyKindLegacy, []resources.Stage{resources.StageBuild, resources.StageRun}},
		{resources.DependencyKindBuild, []resources.Stage{resources.StageBuild}},
		{resources.DependencyKindSchema, []resources.Stage{resources.StageBuild}},
		{resources.DependencyKindRuntime, []resources.Stage{resources.StageRun}},
		{resources.DependencyKindCompletion, []resources.Stage{resources.StageRun}},
		{resources.DependencyKindExternal, nil},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			require.NoError(t, tc.kind.Validate())
			require.Equal(t, tc.stages, tc.kind.Stages())
			for _, stage := range resources.Stages() {
				require.Equal(t, tc.kind.Participates(stage), containsStage(tc.stages, stage), "stage %s", stage)
			}
		})
	}
}

func containsStage(stages []resources.Stage, stage resources.Stage) bool {
	for _, s := range stages {
		if s == stage {
			return true
		}
	}
	return false
}

// A phase is what a caller asks for; a stage is a sortable graph. Testing or
// deploying builds first, so a build input constrains those phases even though
// it constrains no run.
func TestPhaseDecomposesIntoStages(t *testing.T) {
	require.Equal(t, []resources.Stage{resources.StageBuild}, resources.PhaseBuild.Stages())
	require.Equal(t, []resources.Stage{resources.StageRun}, resources.PhaseRun.Stages())
	require.Equal(t, []resources.Stage{resources.StageBuild, resources.StageRun}, resources.PhaseTest.Stages())
	require.Equal(t, []resources.Stage{resources.StageBuild, resources.StageRun}, resources.PhaseDeploy.Stages())

	require.True(t, resources.DependencyKindBuild.ConstrainsPhase(resources.PhaseTest))
	require.True(t, resources.DependencyKindBuild.ConstrainsPhase(resources.PhaseDeploy))
	require.False(t, resources.DependencyKindBuild.ConstrainsPhase(resources.PhaseRun))
	require.False(t, resources.DependencyKindExternal.ConstrainsPhase(resources.PhaseTest))
}

// The tables are package state shared by every caller; handing out the live
// slice lets one caller corrupt every later lookup.
func TestKindAndPhaseTablesAreNotAliased(t *testing.T) {
	stages := resources.DependencyKindBuild.Stages()
	stages[0] = resources.StageRun
	require.Equal(t, []resources.Stage{resources.StageBuild}, resources.DependencyKindBuild.Stages())

	phaseStages := resources.PhaseTest.Stages()
	phaseStages[0] = resources.StageRun
	require.Equal(t, []resources.Stage{resources.StageBuild, resources.StageRun}, resources.PhaseTest.Stages())
}

func TestDependencyKindPrerequisite(t *testing.T) {
	// A one-shot prerequisite and a continuously running one are not
	// interchangeable: waiting on the wrong one either never succeeds or
	// succeeds too early.
	require.Equal(t, resources.PrerequisiteEndpointHealth, resources.DependencyKindLegacy.Prerequisite())
	require.Equal(t, resources.PrerequisiteEndpointHealth, resources.DependencyKindRuntime.Prerequisite())
	require.Equal(t, resources.PrerequisiteCompletion, resources.DependencyKindCompletion.Prerequisite())
	require.Equal(t, resources.PrerequisiteNone, resources.DependencyKindBuild.Prerequisite())
	require.Equal(t, resources.PrerequisiteNone, resources.DependencyKindSchema.Prerequisite())
	require.Equal(t, resources.PrerequisiteNone, resources.DependencyKindExternal.Prerequisite())
}

func TestParsePhase(t *testing.T) {
	phase, err := resources.ParsePhase("deploy")
	require.NoError(t, err)
	require.Equal(t, resources.PhaseDeploy, phase)

	_, err = resources.ParsePhase("provision")
	require.Error(t, err)
}

func TestServiceDependencyValidate(t *testing.T) {
	unknown := &resources.ServiceDependency{Name: "api", Module: "web", Kind: "startup"}
	require.ErrorContains(t, unknown.Validate(), "unknown dependency kind")

	oneShot := &resources.ServiceDependency{
		Name:      "migration",
		Module:    "data",
		Kind:      resources.DependencyKindCompletion,
		Endpoints: []*resources.EndpointReference{{Name: "grpc"}},
	}
	require.ErrorContains(t, oneShot.Validate(), "cannot consume endpoints")

	require.NoError(t, (&resources.ServiceDependency{Name: "api", Module: "web"}).Validate())
	require.NoError(t, (&resources.ServiceDependency{
		Name:      "api",
		Module:    "web",
		Kind:      resources.DependencyKindSchema,
		Endpoints: []*resources.EndpointReference{{Name: "grpc"}},
	}).Validate())
}

func writeService(t *testing.T, dir string, content string) string {
	t.Helper()
	serviceDir := filepath.Join(dir, "service")
	require.NoError(t, os.MkdirAll(serviceDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(serviceDir, resources.ServiceConfigurationName), []byte(content), 0o600))
	return serviceDir
}

func TestServiceDependencyKindRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := writeService(t, t.TempDir(), `kind: service
name: worker
version: 0.0.0
agent:
    kind: runtime::service
    name: go-grpc
    version: 0.0.1
    publisher: codefly.ai
service-dependencies:
    - name: api
      module: web
      kind: build
    - name: migration
      module: data
      kind: completion
    - name: legacy
      module: web
`)
	svc, err := resources.LoadServiceFromDir(ctx, dir)
	require.NoError(t, err)
	require.Len(t, svc.ServiceDependencies, 3)
	require.Equal(t, resources.DependencyKindBuild, svc.ServiceDependencies[0].Kind)
	require.Equal(t, resources.DependencyKindCompletion, svc.ServiceDependencies[1].Kind)
	require.Equal(t, resources.DependencyKindLegacy, svc.ServiceDependencies[2].Kind)

	require.True(t, svc.ServiceDependencies[0].Participates(resources.StageBuild))
	require.False(t, svc.ServiceDependencies[0].Participates(resources.StageRun))
	require.Equal(t, resources.PrerequisiteCompletion, svc.ServiceDependencies[1].Prerequisite())

	require.NoError(t, svc.Save(ctx))
	reloaded, err := resources.LoadServiceFromDir(ctx, dir)
	require.NoError(t, err)
	require.Equal(t, svc.ServiceDependencies[0].Kind, reloaded.ServiceDependencies[0].Kind)
	require.Equal(t, svc.ServiceDependencies[1].Kind, reloaded.ServiceDependencies[1].Kind)
	require.Equal(t, resources.DependencyKindLegacy, reloaded.ServiceDependencies[2].Kind)
}

func TestServiceDependencyInvalidKindFailsLoad(t *testing.T) {
	ctx := context.Background()
	dir := writeService(t, t.TempDir(), `kind: service
name: worker
version: 0.0.0
agent:
    kind: runtime::service
    name: go-grpc
    version: 0.0.1
    publisher: codefly.ai
service-dependencies:
    - name: api
      module: web
      kind: whenever
`)
	_, err := resources.LoadServiceFromDir(ctx, dir)
	require.ErrorContains(t, err, "unknown dependency kind")
}

func TestTypedDependencyRejectsAbsentEndpoint(t *testing.T) {
	// A declared kind does not loosen reference checking: a schema edge that
	// names an endpoint the producer does not export is still rejected.
	dependency := &resources.ServiceDependency{
		Name:      "api",
		Module:    "web",
		Kind:      resources.DependencyKindSchema,
		Endpoints: []*resources.EndpointReference{{Name: "graphql"}},
	}
	endpoints := []*basev0.Endpoint{{Module: "web", Service: "api", Name: "grpc", Api: standards.GRPC}}
	require.ErrorContains(t, resources.ValidateServiceDependencyEndpoints(dependency, endpoints), "undeclared")
}

// A binary that does not model a dependency key must not erase it. Without a
// catch-all on ServiceDependency, a load → mutate → Save round-trip through an
// older core silently deleted `kind` from disk, turning a phase-typed edge back
// into a legacy one that constrains everything — reintroducing the very cycle
// kinds exist to remove. Service.ExtraFields exists for the same reason.
func TestUnknownDependencyKeySurvivesRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := writeService(t, t.TempDir(), `kind: service
name: worker
version: 0.0.0
agent:
    kind: runtime::service
    name: go-grpc
    version: 0.0.1
    publisher: codefly.ai
service-dependencies:
    - name: api
      module: web
      kind: build
      some-future-key: hello
`)
	svc, err := resources.LoadServiceFromDir(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, svc.Save(ctx))

	out, err := os.ReadFile(filepath.Join(dir, resources.ServiceConfigurationName))
	require.NoError(t, err)
	require.Contains(t, string(out), "some-future-key: hello")
	require.Contains(t, string(out), "kind: build")
}

// The counterpart to rejecting a completion dependency that consumes endpoints:
// a dependency that waits for endpoint health onto a producer exporting none
// waits forever.
func TestRuntimeDependencyOntoEndpointlessProducerIsRejected(t *testing.T) {
	dependency := &resources.ServiceDependency{
		Name:   "migration",
		Module: "data",
		Kind:   resources.DependencyKindRuntime,
	}
	err := resources.ValidateDependencyPrerequisite(dependency, nil)
	require.ErrorContains(t, err, "exports no endpoint")
	require.ErrorContains(t, err, string(resources.DependencyKindCompletion))

	// A producer that does export endpoints is fine.
	require.NoError(t, resources.ValidateDependencyPrerequisite(dependency,
		[]*basev0.Endpoint{{Module: "data", Service: "migration", Name: "grpc", Api: standards.GRPC}}))

	// Legacy dependencies are how existing workspaces express one-shot work.
	legacy := &resources.ServiceDependency{Name: "migration", Module: "data"}
	require.NoError(t, resources.ValidateDependencyPrerequisite(legacy, nil))

	// A completion dependency is the correct declaration and is never checked
	// for endpoint health.
	completion := &resources.ServiceDependency{Name: "migration", Module: "data", Kind: resources.DependencyKindCompletion}
	require.NoError(t, resources.ValidateDependencyPrerequisite(completion, nil))
}

// Applications carry the same ServiceDependency type as services and so must
// reject the same invalid declarations.
func TestApplicationRejectsUnknownDependencyKind(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.ApplicationConfigurationName), []byte(`kind: application
name: console
version: 0.0.1
agent:
    kind: runtime::service
    name: go-grpc
    version: 0.0.1
    publisher: codefly.ai
service-dependencies:
    - name: api
      module: web
      kind: whenever
`), 0o600))
	_, err := resources.LoadApplicationFromDir(ctx, dir)
	require.ErrorContains(t, err, "unknown dependency kind")
}

// A network mapping is a runtime address, so only dependencies that constrain
// the run stage may consume one. Injecting a build-only producer's connection
// hands a consumer credentials for a service it never talks to.
func TestNetworkMappingsSkipNonRuntimeDependencies(t *testing.T) {
	mappings := []*basev0.NetworkMapping{{
		Endpoint: &basev0.Endpoint{Module: "web", Service: "api", Name: "grpc", Api: standards.GRPC},
	}}

	runtime := []*resources.ServiceDependency{{Name: "api", Module: "web", Kind: resources.DependencyKindRuntime}}
	resolved, err := resources.ResolveDependencyNetworkMappings(runtime, mappings)
	require.NoError(t, err)
	require.Len(t, resolved, 1)

	for _, kind := range []resources.DependencyKind{resources.DependencyKindBuild, resources.DependencyKindSchema, resources.DependencyKindExternal} {
		deps := []*resources.ServiceDependency{{Name: "api", Module: "web", Kind: kind}}
		resolved, err := resources.ResolveDependencyNetworkMappings(deps, mappings)
		require.NoError(t, err, "kind %s", kind)
		require.Empty(t, resolved, "kind %s must not consume a runtime address", kind)
	}

	// Legacy keeps consuming mappings exactly as before.
	legacy := []*resources.ServiceDependency{{Name: "api", Module: "web"}}
	resolved, err = resources.ResolveDependencyNetworkMappings(legacy, mappings)
	require.NoError(t, err)
	require.Len(t, resolved, 1)
}
