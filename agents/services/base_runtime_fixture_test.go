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

// reload reproduces what Base.Load does to an agent that is loaded a second
// time: the environment manager is replaced, while the wrapper built by
// NewServiceBase survives for the life of the agent process.
func reload(wrapper *RuntimeWrapper) {
	wrapper.EnvironmentVariables = resources.NewEnvironmentVariableManager()
}

func serviceEnvironment(t *testing.T, wrapper *RuntimeWrapper) []string {
	t.Helper()
	variables, err := wrapper.EnvironmentVariables.All()
	require.NoError(t, err)
	return resources.EnvironmentVariableAsStrings(variables)
}

func requireNoFixture(t *testing.T, wrapper *RuntimeWrapper) {
	t.Helper()
	for _, variable := range serviceEnvironment(t, wrapper) {
		require.NotContains(t, variable, "CODEFLY__FIXTURE")
	}
}

// A service under test in START_DEPENDENCIES mode has its Start replaced by a
// no-op barrier, so Init is the only call that carries the selection to it.
func TestRuntimeFixtureFromInitReachesAServiceThatIsNeverStarted(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixtureFromInit((&runtimev0.InitRequest{Fixture: "dev-admin"}).GetFixture())

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=dev-admin")
}

func TestRuntimeFixtureSurvivesAStartThatCarriesNoSelection(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixtureFromInit((&runtimev0.InitRequest{Fixture: "dev-admin"}).GetFixture())
	wrapper.SetFixtureFromStart((&runtimev0.StartRequest{}).GetFixture())

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=dev-admin")
}

// An agent host that populates only StartRequest keeps working unchanged.
func TestRuntimeFixtureFallsBackToStartWhenInitCarriesNone(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixtureFromInit((&runtimev0.InitRequest{}).GetFixture())
	wrapper.SetFixtureFromStart((&runtimev0.StartRequest{Fixture: "dev-admin"}).GetFixture())

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=dev-admin")
}

func TestRuntimeFixtureKeepsTheInitSelectionOverStart(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixtureFromInit((&runtimev0.InitRequest{Fixture: "dev-admin"}).GetFixture())
	wrapper.SetFixtureFromStart((&runtimev0.StartRequest{Fixture: "other"}).GetFixture())

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=dev-admin")
	require.NotContains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=other")
}

func TestRuntimeFixtureAbsentWhenNoSelectionIsMade(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixtureFromInit((&runtimev0.InitRequest{}).GetFixture())

	requireNoFixture(t, wrapper)
}

// Load replaces the environment manager but not the wrapper. A selection
// remembered on the wrapper would make the second Init a no-op and start the
// service with no fixture at all — the very bug carrying it on Init fixes.
func TestRuntimeFixtureIsRestampedOnAnAgentThatIsLoadedAgain(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixtureFromInit((&runtimev0.InitRequest{Fixture: "dev-admin"}).GetFixture())
	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=dev-admin")

	reload(wrapper)
	wrapper.SetFixtureFromInit((&runtimev0.InitRequest{Fixture: "dev-admin"}).GetFixture())

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=dev-admin")
}

// Agents advertising HOT_RELOAD are re-inited without restarting, so one live
// process serves invocations that select different fixtures. Serving the first
// one is worse than serving none: it seeds the wrong data and looks healthy.
func TestRuntimeFixtureChangesWhenAReusedAgentIsInitedAgain(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixtureFromInit((&runtimev0.InitRequest{Fixture: "dev-admin"}).GetFixture())
	wrapper.SetFixtureFromInit((&runtimev0.InitRequest{Fixture: "read-only"}).GetFixture())

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=read-only")
	require.NotContains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=dev-admin")
}

func TestRuntimeFixtureIsClearedWhenAReusedAgentIsInitedWithoutOne(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixtureFromInit((&runtimev0.InitRequest{Fixture: "dev-admin"}).GetFixture())
	wrapper.SetFixtureFromInit((&runtimev0.InitRequest{}).GetFixture())

	requireNoFixture(t, wrapper)
}

// An agent that adopted the Init call but still stamps Start through the
// environment manager directly must not wipe the invocation's selection.
func TestRuntimeFixtureSurvivesADirectEnvironmentStampFromStart(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetFixtureFromInit((&runtimev0.InitRequest{Fixture: "dev-admin"}).GetFixture())
	wrapper.EnvironmentVariables.SetFixture((&runtimev0.StartRequest{}).GetFixture())

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__FIXTURE=dev-admin")
}

// The runtime-context accessors in this file are deliberately nil-safe, and
// core itself builds a zero-value wrapper as a fallback (grpc.go). Stamping a
// fixture must not be the one lifecycle call that crashes the agent.
func TestRuntimeFixtureStampingIsNilSafe(t *testing.T) {
	require.NotPanics(t, func() {
		(&RuntimeWrapper{}).SetFixtureFromInit("dev-admin")
		(&RuntimeWrapper{}).SetFixtureFromStart("dev-admin")
	})
	require.NotPanics(t, func() {
		(&RuntimeWrapper{Base: &Base{}}).SetFixtureFromInit("dev-admin")
		(&RuntimeWrapper{Base: &Base{}}).SetFixtureFromStart("dev-admin")
	})
}
