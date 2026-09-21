//go:build darwin || linux

package composition

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectionActivationRetainsSupersededSymlink(t *testing.T) {
	root := t.TempDir()
	destination, candidate := filepath.Join(root, "active"), filepath.Join(root, "candidate")
	require.NoError(t, os.Symlink("old-revision", destination))
	require.NoError(t, os.Symlink("new-revision", candidate))
	old, err := os.Lstat(destination)
	require.NoError(t, err)
	require.NoError(t, activateProjectionLink(candidate, destination))
	retained, err := os.Lstat(candidate)
	require.NoError(t, err)
	require.True(t, os.SameFile(old, retained))
	target, err := os.Readlink(candidate)
	require.NoError(t, err)
	require.Equal(t, "old-revision", target)
	target, err = os.Readlink(destination)
	require.NoError(t, err)
	require.Equal(t, "new-revision", target)
}

func TestProjectionActivationMissingCandidatePreservesDestination(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "active")
	require.NoError(t, os.Symlink("old-revision", destination))
	old, err := os.Lstat(destination)
	require.NoError(t, err)
	require.ErrorIs(t, activateProjectionLink(filepath.Join(root, "missing"), destination), os.ErrNotExist)
	retained, err := os.Lstat(destination)
	require.NoError(t, err)
	require.True(t, os.SameFile(old, retained))
	target, err := os.Readlink(destination)
	require.NoError(t, err)
	require.Equal(t, "old-revision", target)
}
