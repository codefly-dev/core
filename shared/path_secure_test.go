//go:build !windows

package shared_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

// File-mode tests are POSIX-shaped. The `//go:build !windows` tag at the
// top excludes this file on Windows entirely — compile-time exclusion
// instead of a runtime t.Skip, which would mask real regressions.

func TestCheckDirectoryOrCreateSecure_CreatesAt0700(t *testing.T) {

	dir := filepath.Join(t.TempDir(), "secrets")
	created, err := shared.CheckDirectoryOrCreateSecure(context.Background(), dir)
	require.NoError(t, err)
	require.True(t, created, "expected directory to be created on first call")

	info, err := os.Stat(dir)
	require.NoError(t, err)
	// 0700 = owner rwx only. fs.ModePerm masks off the directory bit
	// so we compare with the literal mode constant.
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm(),
		"secure directory must be owner-only (0700)")
}

func TestCheckDirectoryOrCreateSecure_IdempotentExistingDir(t *testing.T) {

	dir := filepath.Join(t.TempDir(), "already-here")
	require.NoError(t, os.Mkdir(dir, 0o755))

	// Second call returns false (not created) and doesn't error,
	// even though the existing dir is 0o755 (we don't tighten).
	created, err := shared.CheckDirectoryOrCreateSecure(context.Background(), dir)
	require.NoError(t, err)
	require.False(t, created)
}

func TestCheckDirectoryOrCreate_DefaultIs0755(t *testing.T) {

	dir := filepath.Join(t.TempDir(), "regular")
	created, err := shared.CheckDirectoryOrCreate(context.Background(), dir)
	require.NoError(t, err)
	require.True(t, created)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	// 0755 = world-readable. Confirms the default helper is
	// deliberately less strict than the secure variant.
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestWriteFileSecure_WritesAt0600(t *testing.T) {

	path := filepath.Join(t.TempDir(), "secrets", "token")
	require.NoError(t, shared.WriteFileSecure(context.Background(), path, []byte("supersecret")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"secret file must be owner-only (0600)")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte("supersecret"), got)
}

func TestWriteFileSecure_IsAtomic(t *testing.T) {

	// Verify the rename behavior: writing the same path twice doesn't
	// leave behind tmp files in the directory. (Catches a regression
	// where the atomic-write helper might leak `.codefly-secret-*`
	// files on partial failure.)
	dir := t.TempDir()
	path := filepath.Join(dir, "key")

	require.NoError(t, shared.WriteFileSecure(context.Background(), path, []byte("v1")))
	require.NoError(t, shared.WriteFileSecure(context.Background(), path, []byte("v2")))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "expected only the final file, no leftover tmp files: %+v", entries)
	require.Equal(t, "key", entries[0].Name())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte("v2"), got, "second write must replace first atomically")
}

func TestWriteFileSecure_CreatesParentDirAt0700(t *testing.T) {

	root := t.TempDir()
	path := filepath.Join(root, "nested", "deeper", "secret")
	require.NoError(t, shared.WriteFileSecure(context.Background(), path, []byte("x")))

	// Both intermediate dirs should exist; the leaf one (which the
	// helper actually created) should be 0o700.
	info, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}
