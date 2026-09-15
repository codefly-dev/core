package resources

import (
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentVariableManagerExposesNamingScopeAsRuntimeIdentity(t *testing.T) {
	holder := NewEnvironmentVariableManager()
	holder.SetEnvironment(&basev0.Environment{
		Name:        "production",
		NamingScope: "stable-eu",
		Fixture:     "dev-admin",
	})

	variables, err := holder.getBase()
	require.NoError(t, err)
	require.Contains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__ENVIRONMENT=production")
	require.Contains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__NAMING_SCOPE=stable-eu")
	require.Contains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__FIXTURE=dev-admin")
}

func TestEnvironmentVariableManagerOmitsEmptyNamingScope(t *testing.T) {
	holder := NewEnvironmentVariableManager()
	holder.SetEnvironment(&basev0.Environment{Name: "local"})

	variables, err := holder.getBase()
	require.NoError(t, err)
	require.NotContains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__NAMING_SCOPE=")
	require.NotContains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__FIXTURE=")
}

func TestEnvironmentVariableManagerExplicitFixtureOverridesEnvironmentFixture(t *testing.T) {
	holder := NewEnvironmentVariableManager()
	holder.SetEnvironment(&basev0.Environment{Name: "local", Fixture: "workspace-fixture"})
	holder.SetFixture("invocation-fixture")

	variables, err := holder.getBase()
	require.NoError(t, err)
	require.Contains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__FIXTURE=invocation-fixture")
	require.NotContains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__FIXTURE=workspace-fixture")
}

func TestEnvironmentVariableManagerSetFixtureNeitherClearsNorOverrides(t *testing.T) {
	holder := NewEnvironmentVariableManager()
	holder.SetFixture("invocation-fixture")

	holder.SetFixture("")
	holder.SetFixture("weaker-source")

	variables, err := holder.getBase()
	require.NoError(t, err)
	require.Contains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__FIXTURE=invocation-fixture")
}

func TestEnvironmentVariableManagerSetOverridesNeitherClearsNorOverrides(t *testing.T) {
	holder := NewEnvironmentVariableManager()
	holder.SetOverrides(map[string]string{"CODEFLY__API_CONSUMES": "invocation"})

	holder.SetOverrides(nil)
	holder.SetOverrides(map[string]string{"CODEFLY__API_CONSUMES": "weaker-source"})

	variables, err := holder.getBase()
	require.NoError(t, err)
	require.Contains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__API_CONSUMES=invocation")
	require.NotContains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__API_CONSUMES=weaker-source")
}

func TestEnvironmentVariableManagerResetOverridesReplacesAndClears(t *testing.T) {
	holder := NewEnvironmentVariableManager()
	holder.ResetOverrides(map[string]string{"CODEFLY__API_CONSUMES": "first-invocation"})
	holder.ResetOverrides(map[string]string{"CODEFLY__API_CONSUMES": "second-invocation"})

	variables, err := holder.getBase()
	require.NoError(t, err)
	require.Contains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__API_CONSUMES=second-invocation")
	require.NotContains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__API_CONSUMES=first-invocation")

	holder.ResetOverrides(nil)

	variables, err = holder.getBase()
	require.NoError(t, err)
	require.NotContains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__API_CONSUMES=second-invocation")
}

// The manager outlives an invocation whenever an agent process is reused, so
// the authoritative source has to be able to select a different fixture, or
// none at all.
func TestEnvironmentVariableManagerResetFixtureReplacesAndClears(t *testing.T) {
	holder := NewEnvironmentVariableManager()
	holder.ResetFixture("first-invocation")
	holder.ResetFixture("second-invocation")

	variables, err := holder.getBase()
	require.NoError(t, err)
	require.Contains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__FIXTURE=second-invocation")
	require.NotContains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__FIXTURE=first-invocation")

	holder.ResetFixture("")

	variables, err = holder.getBase()
	require.NoError(t, err)
	require.NotContains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__FIXTURE=second-invocation")
}
