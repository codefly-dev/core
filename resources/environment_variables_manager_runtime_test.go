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
	})

	variables, err := holder.getBase()
	require.NoError(t, err)
	require.Contains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__ENVIRONMENT=production")
	require.Contains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__NAMING_SCOPE=stable-eu")
}

func TestEnvironmentVariableManagerOmitsEmptyNamingScope(t *testing.T) {
	holder := NewEnvironmentVariableManager()
	holder.SetEnvironment(&basev0.Environment{Name: "local"})

	variables, err := holder.getBase()
	require.NoError(t, err)
	require.NotContains(t, EnvironmentVariableAsStrings(variables), "CODEFLY__NAMING_SCOPE=")
}
