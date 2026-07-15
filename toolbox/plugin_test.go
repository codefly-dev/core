package toolbox_test

import (
	"testing"

	"github.com/codefly-dev/core/toolbox"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentHelpers(t *testing.T) {
	t.Setenv(toolbox.VersionEnvironment, "")
	require.Equal(t, "0.0.0-dev", toolbox.Version())

	t.Setenv(toolbox.VersionEnvironment, "1.2.3")
	require.Equal(t, "1.2.3", toolbox.Version())

	t.Setenv("CODEFLY_TEST_LIST", " one, ,two , three")
	require.Equal(t, []string{"one", "two", "three"}, toolbox.EnvironmentList("CODEFLY_TEST_LIST"))
}
