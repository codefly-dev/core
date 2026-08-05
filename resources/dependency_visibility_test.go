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
		allowed    []string
		deny       bool
		errorText  string
	}{
		{name: "private cross-module denied", consumer: "platform", producer: "saas", visibility: resources.VisibilityPrivate, deny: true, errorText: "private to module"},
		{name: "empty visibility cross-module denied", consumer: "platform", producer: "saas", visibility: "", deny: true, errorText: "private to module"},
		{name: "private same-module allowed", consumer: "saas", producer: "saas", visibility: resources.VisibilityPrivate},
		{name: "internal listed module allowed", consumer: "platform", producer: "saas", visibility: resources.VisibilityInternal, allowed: []string{"platform"}},
		{name: "internal wildcard allowed", consumer: "platform", producer: "saas", visibility: resources.VisibilityInternal, allowed: []string{resources.AllowAllModules}},
		{name: "internal unlisted module denied", consumer: "web", producer: "saas", visibility: resources.VisibilityInternal, allowed: []string{"platform"}, deny: true, errorText: "does not permit module"},
		{name: "internal empty allow-list denied", consumer: "platform", producer: "saas", visibility: resources.VisibilityInternal, deny: true, errorText: "does not permit module"},
		{name: "module cross-module allowed", consumer: "platform", producer: "saas", visibility: resources.VisibilityModule},
		{name: "public cross-module allowed", consumer: "platform", producer: "saas", visibility: resources.VisibilityPublic},
		{name: "external cross-module allowed", consumer: "platform", producer: "saas", visibility: resources.VisibilityExternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := resources.ValidateEndpointVisibility(tc.consumer, tc.producer, "accounts", "connect", tc.visibility, tc.allowed)
			if tc.deny {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errorText)
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

func TestValidateServiceDependenciesUnresolvedProducer(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/unresolved-dependency-visibility")
	require.NoError(t, err)
	// Dependencies on a module absent from the workspace and on a service
	// absent from a known module are the resolver's concern, not this pass's:
	// with nothing to judge, visibility validation must not error.
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
