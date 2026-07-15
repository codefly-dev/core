package shared_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

func TestFileExists(t *testing.T) {
	p, err := shared.SolvePath("testdata/file.txt")
	require.NoError(t, err)
	require.True(t, shared.Must(shared.FileExists(context.Background(), p)))
}

func TestCheckEmptyDirectoryOrCreate(t *testing.T) {
	t.Run("creates missing directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "new")

		created, err := shared.CheckEmptyDirectoryOrCreate(context.Background(), dir)

		require.NoError(t, err)
		require.True(t, created)
		info, err := os.Stat(dir)
		require.NoError(t, err)
		require.True(t, info.IsDir())
	})

	t.Run("accepts existing empty directory", func(t *testing.T) {
		dir := t.TempDir()

		created, err := shared.CheckEmptyDirectoryOrCreate(context.Background(), dir)

		require.NoError(t, err)
		require.False(t, created)
	})

	t.Run("rejects existing non-empty directory", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "data"), []byte("occupied"), 0600))

		created, err := shared.CheckEmptyDirectoryOrCreate(context.Background(), dir)

		require.ErrorContains(t, err, "directory is not empty")
		require.False(t, created)
	})
}
