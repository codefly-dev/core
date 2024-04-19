package resources_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"

	"github.com/stretchr/testify/require"
)

func TestValidation(t *testing.T) {
	ctx := context.Background()

	tcs := []struct {
		name      string
		workspace string
	}{
		{"normal", "workspace"},
		{"with -", "my-workspace"},
		{"ending in 0", "my-workspace0"},
		{"ending in -0", "my-workspace-0"},
		{"start with 0", "0-workspace"},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resources.NewWorkspace(ctx, tc.workspace, resources.LayoutKindFlat)
			require.NoError(t, err)
		})
	}

	tcs = []struct {
		name      string
		workspace string
	}{
		{"too short", "wo"},
		{"with _", "my_workspace"},
		{"with .", "my.workspace"},
		{"with spaces", "my workspace"},
		{"with two --", "my--workspace"},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resources.NewWorkspace(ctx, tc.workspace, resources.LayoutKindFlat)
			require.Error(t, err)
		})
	}
}

func TestWorkspaceFlatLayout(t *testing.T) {
	testWorkspace(t, "testdata/workspaces/flat-layout")
}

func TestWorkspaceModuleLayout(t *testing.T) {
	ctx := context.Background()
	ws := testWorkspace(t, "testdata/workspaces/module-layout")
	// Also find by full name
	svc, err := ws.FindUniqueServiceByName(ctx, "mod/svc")
	require.NoError(t, err)
	require.NotNil(t, svc)
}

func testWorkspace(t *testing.T, dir string) *resources.Workspace {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	require.NotNil(t, workspace)
	svc, err := workspace.FindUniqueServiceByName(ctx, "svc")
	require.NoError(t, err)
	require.NotNil(t, svc)
	return workspace
}
