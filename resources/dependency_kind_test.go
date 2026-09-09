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

func TestDependencyKindPhases(t *testing.T) {
	cases := []struct {
		kind   resources.DependencyKind
		phases []resources.Phase
	}{
		{resources.DependencyKindLegacy, []resources.Phase{resources.PhaseBuild, resources.PhaseRun, resources.PhaseTest, resources.PhaseDeploy}},
		{resources.DependencyKindBuild, []resources.Phase{resources.PhaseBuild}},
		{resources.DependencyKindSchema, []resources.Phase{resources.PhaseBuild}},
		{resources.DependencyKindRuntime, []resources.Phase{resources.PhaseRun, resources.PhaseTest, resources.PhaseDeploy}},
		{resources.DependencyKindCompletion, []resources.Phase{resources.PhaseRun, resources.PhaseTest, resources.PhaseDeploy}},
		{resources.DependencyKindExternal, nil},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			require.NoError(t, tc.kind.Validate())
			require.Equal(t, tc.phases, tc.kind.Phases())
			for _, phase := range resources.Phases() {
				require.Equal(t, tc.kind.Participates(phase), containsPhase(tc.phases, phase), "phase %s", phase)
			}
		})
	}
}

func containsPhase(phases []resources.Phase, phase resources.Phase) bool {
	for _, p := range phases {
		if p == phase {
			return true
		}
	}
	return false
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

	require.True(t, svc.ServiceDependencies[0].Participates(resources.PhaseBuild))
	require.False(t, svc.ServiceDependencies[0].Participates(resources.PhaseRun))
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
