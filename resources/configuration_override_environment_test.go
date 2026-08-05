package resources_test

import (
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestInvocationConfigurationCarriersDoNotCrossAgentBoundary(t *testing.T) {
	environment := []string{
		"PATH=/usr/bin",
		resources.WorkspaceConfigurationOverridesEnvironment + "=workspace-secret",
		resources.ServiceConfigurationOverridesEnvironment + "=service-secret",
		"CODEFLY_EPHEMERAL_CONTAINERS=123",
	}

	require.Equal(t, []string{
		"PATH=/usr/bin",
		"CODEFLY_EPHEMERAL_CONTAINERS=123",
	}, resources.WithoutInvocationConfigurationOverrides(environment))
}
