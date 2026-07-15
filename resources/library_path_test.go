package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestLibraryRejectsEscapingNamesAndLanguagePaths(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"../escape", "nested/library", `nested\library`, ".", ".."} {
		_, err := resources.NewLibrary(ctx, name)
		require.Error(t, err, name)
	}

	lib, err := resources.NewLibrary(ctx, "shared-models")
	require.NoError(t, err)
	require.Error(t, lib.AddLanguage(ctx, "go", "", "../outside"))
	require.Error(t, lib.AddLanguage(ctx, "../go", "", "go"))
}

func TestCreateLibraryValidatesBeforeCreatingDirectories(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workspace := &resources.Workspace{Name: "workspace", Layout: resources.LayoutKindModules}
	require.NoError(t, workspace.SaveToDirUnsafe(ctx, root))

	_, err := workspace.CreateLibrary(ctx, "../escape", []string{"go"})
	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(root, "escape"))
	require.ErrorIs(t, statErr, os.ErrNotExist)

	_, err = workspace.CreateLibrary(ctx, "valid-library", []string{"../escape"})
	require.Error(t, err)
	_, statErr = os.Stat(filepath.Join(root, "libraries", "valid-library"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}
