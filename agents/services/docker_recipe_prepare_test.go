package services

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A two-file template set: the guard has to cover every path the renderer writes,
// not just the Dockerfile.
//
//go:embed testdata/recipetemplates
var recipeTemplates embed.FS

func builderTemplateSet(t *testing.T) fs.FS {
	t.Helper()
	sub, err := fs.Sub(recipeTemplates, "testdata/recipetemplates")
	require.NoError(t, err)
	return sub
}

func TestPrepareRecipeDestinationReportsEveryEmittedPath(t *testing.T) {
	emitted, err := PrepareRecipeDestination(builderTemplateSet(t), t.TempDir())
	require.NoError(t, err)
	require.Equal(t, []string{"Dockerfile", "dockerignore"}, emitted)
}

// The template renderer opens each destination with O_CREATE|O_TRUNC and tests
// existence with os.Stat, both of which follow symlinks — so a symlinked
// destination is written *through*, truncating a file anywhere on disk. Because
// output_directory is the service's committed builder/ directory, such a symlink
// is ordinary repository content. Unlinking every emitted path first is what
// keeps the write inside the caller-owned directory; guarding only "Dockerfile"
// left every other file in the template set escaping.
func TestPrepareRecipeDestinationUnlinksSymlinksWithoutFollowingThem(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "builder")
	require.NoError(t, os.MkdirAll(output, 0o755))

	outside := map[string]string{"Dockerfile": "keep-dockerfile-target", "dockerignore": "keep-ignore-target"}
	for name, content := range outside {
		target := filepath.Join(root, name+".target")
		require.NoError(t, os.WriteFile(target, []byte(content), 0o644))
		require.NoError(t, os.Symlink(target, filepath.Join(output, name)))
	}

	emitted, err := PrepareRecipeDestination(builderTemplateSet(t), output)
	require.NoError(t, err)
	require.Equal(t, []string{"Dockerfile", "dockerignore"}, emitted)

	for name, content := range outside {
		require.NoFileExists(t, filepath.Join(output, name), "the symlink itself must be gone")
		got, readErr := os.ReadFile(filepath.Join(root, name+".target"))
		require.NoError(t, readErr)
		require.Equal(t, content, string(got), "%s target was written through", name)
	}
}

// Content the template set does not own is left alone: the guard unlinks what it
// is about to write, never the directory's other contents.
func TestPrepareRecipeDestinationLeavesUnrelatedContentAlone(t *testing.T) {
	output := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(output, "notes.md"), []byte("hand written"), 0o644))

	_, err := PrepareRecipeDestination(builderTemplateSet(t), output)
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(output, "notes.md"))
	require.NoError(t, err)
	require.Equal(t, "hand written", string(got))
}

func TestPrepareRecipeDestinationRejectsAnEmptyTemplateSet(t *testing.T) {
	_, err := PrepareRecipeDestination(os.DirFS(t.TempDir()), t.TempDir())
	require.Error(t, err)
}
