package resources_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestValidateEndpointVisibility(t *testing.T) {
	cases := []struct {
		name       string
		consumer   string
		producer   string
		visibility resources.Visibility
		deny       bool
	}{
		{name: "private cross-module denied", consumer: "platform", producer: "saas", visibility: resources.VisibilityPrivate, deny: true},
		{name: "empty visibility cross-module denied", consumer: "platform", producer: "saas", visibility: "", deny: true},
		{name: "private same-module allowed", consumer: "saas", producer: "saas", visibility: resources.VisibilityPrivate},
		{name: "module cross-module allowed", consumer: "platform", producer: "saas", visibility: resources.VisibilityModule},
		{name: "public cross-module allowed", consumer: "platform", producer: "saas", visibility: resources.VisibilityPublic},
		{name: "external cross-module allowed", consumer: "platform", producer: "saas", visibility: resources.VisibilityExternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := resources.ValidateEndpointVisibility(tc.consumer, tc.producer, "accounts", "connect", tc.visibility)
			if tc.deny {
				require.Error(t, err)
				require.Contains(t, err.Error(), "private to module")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateServiceDependenciesAllowed(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/allowed-dependency-visibility")
	require.NoError(t, err)
	require.NoError(t, workspace.ValidateServiceDependencies(ctx))
}

func TestValidateServiceDependenciesDenied(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/denied-dependency-visibility")
	require.NoError(t, err)
	err = workspace.ValidateServiceDependencies(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private to module \"saas\"")
	require.Contains(t, err.Error(), "platform")
}
