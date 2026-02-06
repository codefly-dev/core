package resources_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestLoadLibrary(t *testing.T) {
	ctx := context.Background()

	lib, err := resources.LoadLibraryFromDir(ctx, "testdata/workspaces/with-library/libraries/shared-models")
	require.NoError(t, err)
	require.NotNil(t, lib)
	require.Equal(t, "shared-models", lib.Name)
	require.Equal(t, "1.2.0", lib.Version)
	require.Len(t, lib.Languages, 1)
	require.Equal(t, "go", lib.Languages[0].Name)
	require.Equal(t, []string{"github.com/myorg/shared-models"}, lib.Languages[0].Exports)
}

func TestWorkspaceLoadLibrary(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/with-library")
	require.NoError(t, err)
	require.NotNil(t, workspace)

	lib, err := workspace.LoadLibraryFromName(ctx, "shared-models")
	require.NoError(t, err)
	require.NotNil(t, lib)
	require.Equal(t, "shared-models", lib.Name)
}

func TestWorkspaceLoadLibraries(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/with-library")
	require.NoError(t, err)
	require.NotNil(t, workspace)

	libs, err := workspace.LoadLibraries(ctx)
	require.NoError(t, err)
	require.Len(t, libs, 1)
	require.Equal(t, "shared-models", libs[0].Name)
}

func TestServiceWithLibraryDependencies(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/with-library")
	require.NoError(t, err)
	require.NotNil(t, workspace)

	module, err := workspace.LoadModuleFromName(ctx, "with-library")
	require.NoError(t, err)
	require.NotNil(t, module)

	svc, err := module.LoadServiceFromName(ctx, "api")
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.Len(t, svc.LibraryDependencies, 1)
	require.Equal(t, "shared-models", svc.LibraryDependencies[0].Name)
	require.Equal(t, "^1.0.0", svc.LibraryDependencies[0].Version)
	require.Equal(t, []string{"go"}, svc.LibraryDependencies[0].Languages)
}

func TestLibraryResolver_ResolveVersion(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/with-library")
	require.NoError(t, err)

	resolver := resources.NewLibraryResolver(workspace)

	// Test resolving with constraint that matches
	lib, version, err := resolver.ResolveVersion(ctx, "shared-models", "^1.0.0")
	require.NoError(t, err)
	require.NotNil(t, lib)
	require.Equal(t, "1.2.0", version) // Library version is 1.2.0, matches ^1.0.0

	// Test resolving with no constraint
	lib, version, err = resolver.ResolveVersion(ctx, "shared-models", "")
	require.NoError(t, err)
	require.NotNil(t, lib)
	require.Equal(t, "1.2.0", version) // Returns current version

	// Test resolving with constraint that doesn't match
	_, _, err = resolver.ResolveVersion(ctx, "shared-models", "^2.0.0")
	require.Error(t, err) // Version 1.2.0 doesn't satisfy ^2.0.0
}

func TestLibraryResolver_GetLibraryMounts(t *testing.T) {
	ctx := context.Background()

	workspace, err := resources.LoadWorkspaceFromDir(ctx, "testdata/workspaces/with-library")
	require.NoError(t, err)

	module, err := workspace.LoadModuleFromName(ctx, "with-library")
	require.NoError(t, err)

	svc, err := module.LoadServiceFromName(ctx, "api")
	require.NoError(t, err)

	resolver := resources.NewLibraryResolver(workspace)

	mounts, err := resolver.GetLibraryMounts(ctx, svc)
	require.NoError(t, err)
	require.Len(t, mounts, 1)

	mount := mounts[0]
	require.Equal(t, "shared-models", mount.LibraryName)
	require.Equal(t, "go", mount.Language)
	require.Contains(t, mount.SourcePath, "libraries/shared-models/go")
	require.Equal(t, "/libraries/shared-models/go", mount.TargetPath)
	require.Equal(t, []string{"github.com/myorg/shared-models"}, mount.ModulePath)
}

func TestLibraryGetLanguage(t *testing.T) {
	ctx := context.Background()

	lib, err := resources.LoadLibraryFromDir(ctx, "testdata/workspaces/with-library/libraries/shared-models")
	require.NoError(t, err)

	goLang := lib.GetLanguage("go")
	require.NotNil(t, goLang)
	require.Equal(t, "go", goLang.Name)

	pythonLang := lib.GetLanguage("python")
	require.Nil(t, pythonLang)
}

func TestLibraryIdentity(t *testing.T) {
	ctx := context.Background()

	lib, err := resources.LoadLibraryFromDir(ctx, "testdata/workspaces/with-library/libraries/shared-models")
	require.NoError(t, err)

	identity := lib.Identity()
	require.NotNil(t, identity)
	require.Equal(t, "shared-models", identity.Name)
	require.Equal(t, "1.2.0", identity.Version)
}
