package services

import (
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

func fixtureRuntimeWrapper() *RuntimeWrapper {
	base := &Base{EnvironmentVariables: resources.NewEnvironmentVariableManager()}
	wrapper := &RuntimeWrapper{Base: base}
	base.Runtime = wrapper
	return wrapper
}

func serviceEnvironment(t *testing.T, wrapper *RuntimeWrapper) []string {
	t.Helper()
	variables, err := wrapper.EnvironmentVariables.All()
	require.NoError(t, err)
	return resources.EnvironmentVariableAsStrings(variables)
}

// A service under test in START_DEPENDENCIES mode has its Start replaced by a
// no-op barrier, so Init is the only call that carries the selection to it.
func TestRuntimeFixtureFromInitReachesAServiceThatIsNeverStarted(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixture((&runtimev0.InitRequest{Fixture: "dev-admin"}).GetFixture())

	require.Equal(t, "dev-admin", wrapper.Fixture())
	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=dev-admin")
}

func TestRuntimeFixtureSurvivesAStartThatCarriesNoSelection(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixture((&runtimev0.InitRequest{Fixture: "dev-admin"}).GetFixture())
	wrapper.SetFixture((&runtimev0.StartRequest{}).GetFixture())

	require.Equal(t, "dev-admin", wrapper.Fixture())
	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=dev-admin")
}

// An agent host that populates only StartRequest keeps working unchanged.
func TestRuntimeFixtureFallsBackToStartWhenInitCarriesNone(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixture((&runtimev0.InitRequest{}).GetFixture())
	wrapper.SetFixture((&runtimev0.StartRequest{Fixture: "dev-admin"}).GetFixture())

	require.Equal(t, "dev-admin", wrapper.Fixture())
	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=dev-admin")
}

func TestRuntimeFixtureKeepsTheInitSelectionOverStart(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixture((&runtimev0.InitRequest{Fixture: "dev-admin"}).GetFixture())
	wrapper.SetFixture((&runtimev0.StartRequest{Fixture: "other"}).GetFixture())

	require.Equal(t, "dev-admin", wrapper.Fixture())
	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=dev-admin")
	require.NotContains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=other")
}

func TestRuntimeFixtureAbsentWhenNoSelectionIsMade(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixture((&runtimev0.InitRequest{}).GetFixture())

	require.Empty(t, wrapper.Fixture())
	for _, variable := range serviceEnvironment(t, wrapper) {
		require.NotContains(t, variable, "CODEFLY__FIXTURE")
	}
}
