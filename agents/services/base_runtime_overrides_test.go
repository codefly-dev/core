package services

import (
	"testing"

	"github.com/stretchr/testify/require"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

func requireNoOverride(t *testing.T, wrapper *RuntimeWrapper, key string) {
	t.Helper()
	for _, variable := range serviceEnvironment(t, wrapper) {
		require.NotContains(t, variable, key)
	}
}

// A service under test in START_DEPENDENCIES mode has its Start replaced by a
// no-op barrier, so Init is the only call that carries its process overrides —
// the federation inputs a solution entry reads by their exact names.
func TestRuntimeOverridesFromInitReachAServiceThatIsNeverStarted(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetOverridesFromInit((&runtimev0.InitRequest{
		Overrides: map[string]string{"CODEFLY__API_CONSUMES": "documents:wiki/documents/api"},
	}).GetOverrides())

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__API_CONSUMES=documents:wiki/documents/api")
}

func TestRuntimeOverridesSurviveAStartThatCarriesNone(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetOverridesFromInit((&runtimev0.InitRequest{
		Overrides: map[string]string{"CODEFLY__API_CONSUMES": "documents:wiki/documents/api"},
	}).GetOverrides())
	wrapper.SetOverridesFromStart((&runtimev0.StartRequest{}).GetOverrides())

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__API_CONSUMES=documents:wiki/documents/api")
}

// An agent host that populates only StartRequest keeps working unchanged.
func TestRuntimeOverridesFallBackToStartWhenInitCarriesNone(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetOverridesFromInit((&runtimev0.InitRequest{}).GetOverrides())
	wrapper.SetOverridesFromStart((&runtimev0.StartRequest{
		Overrides: map[string]string{"CODEFLY__API_CONSUMES": "documents:wiki/documents/api"},
	}).GetOverrides())

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__API_CONSUMES=documents:wiki/documents/api")
}

// Both calls carrying a value for one key must not leave the process holding
// two entries for it: which one a consumer reads would then depend on the order
// the environment happens to be assembled in.
func TestRuntimeOverridesKeepTheInitValueOverStart(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetOverridesFromInit((&runtimev0.InitRequest{
		Overrides: map[string]string{"CODEFLY__API_CONSUMES": "from-init"},
	}).GetOverrides())
	wrapper.SetOverridesFromStart((&runtimev0.StartRequest{
		Overrides: map[string]string{"CODEFLY__API_CONSUMES": "from-start"},
	}).GetOverrides())

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__API_CONSUMES=from-init")
	require.NotContains(t, serviceEnvironment(t, wrapper), "CODEFLY__API_CONSUMES=from-start")
}

func TestRuntimeOverridesAbsentWhenNoneAreInjected(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetOverridesFromInit((&runtimev0.InitRequest{}).GetOverrides())

	requireNoOverride(t, wrapper, "CODEFLY__API_CONSUMES")
}

// Load replaces the environment manager but not the wrapper, so a second Init
// has to stamp the new manager.
func TestRuntimeOverridesAreRestampedOnAnAgentThatIsLoadedAgain(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()
	overrides := map[string]string{"CODEFLY__API_CONSUMES": "documents:wiki/documents/api"}

	wrapper.SetOverridesFromInit(overrides)
	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__API_CONSUMES=documents:wiki/documents/api")

	reload(wrapper)
	wrapper.SetOverridesFromInit(overrides)

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__API_CONSUMES=documents:wiki/documents/api")
}

// Agents advertising HOT_RELOAD are re-inited without restarting, so one live
// process serves invocations wired to different dependencies. Serving the first
// invocation's federation inputs is worse than serving none.
func TestRuntimeOverridesChangeWhenAReusedAgentIsInitedAgain(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetOverridesFromInit(map[string]string{"CODEFLY__API_CONSUMES": "first"})
	wrapper.SetOverridesFromInit(map[string]string{"CODEFLY__API_CONSUMES": "second"})

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__API_CONSUMES=second")
	require.NotContains(t, serviceEnvironment(t, wrapper), "CODEFLY__API_CONSUMES=first")
}

func TestRuntimeOverridesAreClearedWhenAReusedAgentIsInitedWithoutAny(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetOverridesFromInit(map[string]string{"CODEFLY__API_CONSUMES": "first"})
	wrapper.SetOverridesFromInit((&runtimev0.InitRequest{}).GetOverrides())

	requireNoOverride(t, wrapper, "CODEFLY__API_CONSUMES")
}

// An agent that adopted the Init call but still stamps Start through the
// environment manager directly must not double the invocation's values.
func TestRuntimeOverridesSurviveADirectEnvironmentStampFromStart(t *testing.T) {
	wrapper := fixtureRuntimeWrapper()

	wrapper.SetOverridesFromInit(map[string]string{"CODEFLY__API_CONSUMES": "from-init"})
	wrapper.EnvironmentVariables.SetOverrides((&runtimev0.StartRequest{
		Overrides: map[string]string{"CODEFLY__API_CONSUMES": "from-start"},
	}).GetOverrides())

	require.Contains(t, serviceEnvironment(t, wrapper), "CODEFLY__API_CONSUMES=from-init")
	require.NotContains(t, serviceEnvironment(t, wrapper), "CODEFLY__API_CONSUMES=from-start")
}

// Core itself builds a zero-value wrapper as a fallback (grpc.go), so stamping
// overrides must not be the one lifecycle call that crashes the agent.
func TestRuntimeOverridesStampingIsNilSafe(t *testing.T) {
	overrides := map[string]string{"CODEFLY__API_CONSUMES": "documents:wiki/documents/api"}
	require.NotPanics(t, func() {
		(&RuntimeWrapper{}).SetOverridesFromInit(overrides)
		(&RuntimeWrapper{}).SetOverridesFromStart(overrides)
	})
	require.NotPanics(t, func() {
		(&RuntimeWrapper{Base: &Base{}}).SetOverridesFromInit(overrides)
		(&RuntimeWrapper{Base: &Base{}}).SetOverridesFromStart(overrides)
	})
}
