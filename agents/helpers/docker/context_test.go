package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreparedContextCombinesIgnoreBoundariesAndSeparatesDockerfile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "builder"), 0755))
	for name, data := range map[string]string{
		"builder/Dockerfile":   "FROM scratch\nCOPY . /\n",
		"builder/dockerignore": "!secret\ncustom-secret\nbuilder/\n",
		".dockerignore":        "secret\n",
		"secret":               "root-secret", "custom-secret": "custom-secret", "app": "application",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(data), 0600))
	}
	prepared, err := PrepareBuildContext(context.Background(), root, "builder/Dockerfile", "builder/dockerignore")
	require.NoError(t, err)
	require.FileExists(t, prepared.Dockerfile)
	require.FileExists(t, filepath.Join(prepared.Root, "app"))
	for _, name := range []string{"secret", "custom-secret", "builder/Dockerfile", "builder/dockerignore"} {
		require.NoFileExists(t, filepath.Join(prepared.Root, name))
	}
	require.NoError(t, prepared.Close())
	require.NoDirExists(t, prepared.Root)
	require.FileExists(t, filepath.Join(root, "secret"))
}

func TestPreparedContextUsesRootIgnoreWithoutCustomPolicy(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{"Dockerfile": "FROM scratch", ".dockerignore": "secret", "secret": "hidden"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(data), 0600))
	}
	prepared, err := PrepareBuildContext(context.Background(), root, "Dockerfile", "")
	require.NoError(t, err)
	defer prepared.Close()
	require.NoFileExists(t, filepath.Join(prepared.Root, "secret"))
}

func TestPreparedContextPreservesExecutableAndSymlink(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "app"), []byte("binary"), 0777))
	require.NoError(t, os.Chmod(filepath.Join(root, "app"), 0777))
	require.NoError(t, os.Mkdir(filepath.Join(root, "private"), 0700))
	require.NoError(t, os.Symlink("app", filepath.Join(root, "link")))
	prepared, err := PrepareBuildContext(context.Background(), root, "Dockerfile", "")
	require.NoError(t, err)
	defer prepared.Close()
	info, err := os.Stat(filepath.Join(prepared.Root, "app"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0777), info.Mode().Perm())
	directory, err := os.Stat(filepath.Join(prepared.Root, "private"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), directory.Mode().Perm())
	link, err := os.Readlink(filepath.Join(prepared.Root, "link"))
	require.NoError(t, err)
	require.Equal(t, "app", link)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = PrepareBuildContext(ctx, root, "Dockerfile", "")
	require.ErrorIs(t, err, context.Canceled)
}

func TestPreparedContextDoesNotRecursivelyCopyItsStagingDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch"), 0600))
	prepared, err := PrepareBuildContext(context.Background(), root, "Dockerfile", "")
	require.NoError(t, err)
	defer prepared.Close()
	entries, err := os.ReadDir(prepared.Root)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "Dockerfile", entries[0].Name())
}
