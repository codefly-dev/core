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
