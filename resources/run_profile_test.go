package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/architecture"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceRunProfilesResolveAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	fixture := "testdata/workspaces/run-profiles"
	workspace, err := resources.LoadWorkspaceFromDir(ctx, fixture)
	require.NoError(t, err)

	local, err := workspace.ResolveRunProfile(ctx, "local", resources.RunProfile{})
	require.NoError(t, err)
	require.Equal(t, resources.RunProfile{
		ExcludeDependencies:            []string{"users/accounts"},
		ExcludeWorkspaceConfigurations: []string{"internal-auth"},
	}, local)

	saas, err := workspace.ResolveRunProfile(ctx, "saas", resources.RunProfile{})
	require.NoError(t, err)
	require.Equal(t, resources.RunProfile{}, saas)

	composed, err := workspace.ResolveRunProfile(ctx, "local", resources.RunProfile{
		ExcludeDependencies:            []string{"postgres", "users/accounts"},
		ExcludeWorkspaceConfigurations: []string{"forge-edge-auth", "internal-auth"},
	})
	require.NoError(t, err)
	require.Equal(t, resources.RunProfile{
		ExcludeDependencies:            []string{"storage/postgres", "users/accounts"},
		ExcludeWorkspaceConfigurations: []string{"forge-edge-auth", "internal-auth"},
	}, composed)

	assertRunComposition(t, ctx, workspace, local, []architecture.Service{{Unique: "storage/postgres"}})
	assertRunComposition(t, ctx, workspace, saas, []architecture.Service{
		{Unique: "storage/postgres"},
		{Unique: "users/accounts"},
	})

	root := t.TempDir()
	require.NoError(t, os.CopyFS(root, os.DirFS(fixture)))
	copyWorkspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	require.NoError(t, copyWorkspace.Save(ctx))
	reloaded, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	require.Equal(t, copyWorkspace.RunProfiles, reloaded.RunProfiles)

	serialized, err := os.ReadFile(filepath.Join(root, resources.WorkspaceConfigurationName))
	require.NoError(t, err)
	require.Contains(t, string(serialized), "run-profiles:")
	require.Contains(t, string(serialized), "exclude-workspace-configurations:")
}

func TestWorkspaceRunProfileValidation(t *testing.T) {
	fixture := "testdata/workspaces/run-profiles"
	content, err := os.ReadFile(filepath.Join(fixture, resources.WorkspaceConfigurationName))
	require.NoError(t, err)

	tests := []struct {
		name        string
		old         string
		replacement string
		want        string
	}{
		{
			name:        "empty profile name",
			old:         "  local:",
			replacement: "  \"\":",
			want:        "run profile name cannot be empty",
		},
		{
			name:        "unknown service",
			old:         "users/accounts",
			replacement: "users/missing",
			want:        "unknown service",
		},
		{
			name:        "unknown workspace configuration",
			old:         "internal-auth",
			replacement: "missing-auth",
			want:        "unknown workspace configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.CopyFS(root, os.DirFS(fixture)))
			invalid := strings.Replace(string(content), tt.old, tt.replacement, 1)
			require.NoError(t, os.WriteFile(
				filepath.Join(root, resources.WorkspaceConfigurationName),
				[]byte(invalid),
				0o600,
			))

			_, err := resources.LoadWorkspaceFromDir(context.Background(), root)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestResolveRunProfileRejectsUnknownSelections(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/run-profiles")
	require.NoError(t, err)

	_, err = workspace.ResolveRunProfile(ctx, "missing", resources.RunProfile{})
	require.ErrorContains(t, err, "not declared")

	_, err = workspace.ResolveRunProfile(ctx, "", resources.RunProfile{
		ExcludeDependencies: []string{"users/missing"},
	})
	require.ErrorContains(t, err, "unknown service")

	_, err = workspace.ResolveRunProfile(ctx, "", resources.RunProfile{
		ExcludeWorkspaceConfigurations: []string{"missing-auth"},
	})
	require.ErrorContains(t, err, "unknown workspace configuration")
}

func TestResolveRunProfileNormalizesSelectionName(t *testing.T) {
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/run-profiles")
	require.NoError(t, err)

	// A blank selection (whitespace-only, like an empty name) is treated as "no
	// named profile" rather than an error, matching the empty-string path.
	blank, err := workspace.ResolveRunProfile(ctx, "   ", resources.RunProfile{})
	require.NoError(t, err)
	require.Equal(t, resources.RunProfile{}, blank)

	// A real name with surrounding whitespace still resolves to its profile.
	trimmed, err := workspace.ResolveRunProfile(ctx, "  local  ", resources.RunProfile{})
	require.NoError(t, err)
	require.Equal(t, resources.RunProfile{
		ExcludeDependencies:            []string{"users/accounts"},
		ExcludeWorkspaceConfigurations: []string{"internal-auth"},
	}, trimmed)
}

func assertRunComposition(
	t *testing.T,
	ctx context.Context,
	workspace *resources.Workspace,
	profile resources.RunProfile,
	want []architecture.Service,
) {
	t.Helper()
	dependencies, err := architecture.NewServiceDependencies(
		ctx,
		workspace,
		architecture.ExcludeServices(profile.ExcludeDependencies...),
	)
	require.NoError(t, err)
	order, err := dependencies.OrderTo(ctx, "mind/mind")
	require.NoError(t, err)
	require.ElementsMatch(t, want, order)
}
