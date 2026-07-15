package resources_test

import (
	"context"
	"os"
	"path/filepath"
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

func TestFlatWorkspaceMigrationPreservesServicesAndJobs(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.WorkspaceConfigurationName), []byte(`name: test-workspace
layout: flat
services:
  - name: existing
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.ModuleConfigurationName), []byte(`kind: module
name: test-workspace
services:
  - name: api
jobs:
  - name: migrate
`), 0o600))

	ws, err := resources.LoadWorkspaceFromDir(context.Background(), dir)
	require.NoError(t, err)
	require.Equal(t, []string{"existing", "api"}, serviceReferenceNames(ws.Services))
	require.Equal(t, []string{"migrate"}, jobReferenceNames(ws.Jobs))
	_, err = os.Stat(filepath.Join(dir, resources.ModuleConfigurationName))
	require.True(t, os.IsNotExist(err), "fully migrated legacy module should be removed")

	// Reload from the persisted workspace to prove the migration survived the
	// in-memory object and did not rely on the now-deleted legacy file.
	reloaded, err := resources.LoadWorkspaceFromDir(context.Background(), dir)
	require.NoError(t, err)
	require.Equal(t, []string{"existing", "api"}, serviceReferenceNames(reloaded.Services))
	require.Equal(t, []string{"migrate"}, jobReferenceNames(reloaded.Jobs))
}

func TestFlatWorkspaceMigrationKeepsUnrepresentableLegacyData(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.WorkspaceConfigurationName), []byte(`name: test-workspace
layout: flat
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.ModuleConfigurationName), []byte(`kind: module
name: test-workspace
description: legacy module metadata
services:
  - name: api
applications:
  - name: desktop
`), 0o600))

	ws, err := resources.LoadWorkspaceFromDir(context.Background(), dir)
	require.NoError(t, err)
	require.Equal(t, []string{"api"}, serviceReferenceNames(ws.Services))
	_, err = os.Stat(filepath.Join(dir, resources.ModuleConfigurationName))
	require.NoError(t, err, "legacy file must remain while it contains data Workspace cannot represent")
}

func TestFlatWorkspaceMigrationDoesNotDeleteMalformedLegacyFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.WorkspaceConfigurationName), []byte("name: test-workspace\nlayout: flat\n"), 0o600))
	modulePath := filepath.Join(dir, resources.ModuleConfigurationName)
	require.NoError(t, os.WriteFile(modulePath, []byte("services: [not-valid"), 0o600))

	_, err := resources.LoadWorkspaceFromDir(context.Background(), dir)
	require.Error(t, err)
	got, readErr := os.ReadFile(modulePath)
	require.NoError(t, readErr)
	require.Equal(t, "services: [not-valid", string(got))
}

func serviceReferenceNames(refs []*resources.ServiceReference) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return names
}

func jobReferenceNames(refs []*resources.JobReference) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return names
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
