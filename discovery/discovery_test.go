package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func touch(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, nil, 0o600))
}

// A search never reads the system temporary directory, or anything above it,
// from a directory below it: every test's directory is there, and so is
// whatever another process left behind.
func TestTheTemporaryDirectoryIsACeiling(t *testing.T) {
	shared := t.TempDir()
	t.Setenv("TMPDIR", shared)
	touch(t, filepath.Join(shared, "go.mod"))
	start := filepath.Join(shared, "run", "code", "pkg")
	require.NoError(t, os.MkdirAll(start, 0o755))
	_, found := Find(start, "go.mod", Ceilings)
	require.False(t, found, "the shared temporary directory's go.mod was read")

	touch(t, filepath.Join(shared, "run", "code", "go.mod"))
	directory, found := Find(start, "go.mod", Ceilings)
	require.True(t, found)
	require.Equal(t, filepath.Join(shared, "run", "code"), directory, "below the ceiling, the nearest one is found")
}

// Repository stops at the directory holding .git, a directory or, in a git
// worktree, a file, and still reads that directory.
func TestRepositoryStopsAtTheRepositoryRoot(t *testing.T) {
	outer := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	touch(t, filepath.Join(outer, "codefly.local.yaml"))
	repository := filepath.Join(outer, "repository")
	touch(t, filepath.Join(repository, ".git")) // a worktree's .git is a file
	start := filepath.Join(repository, "workspaces", "notes")
	require.NoError(t, os.MkdirAll(start, 0o755))

	ancestors := Ancestors(start, Repository)
	require.Equal(t, repository, ancestors[len(ancestors)-1], "the repository root is the last directory read")
	_, found := Find(start, "codefly.local.yaml", Repository)
	require.False(t, found, "a file above the repository was read")
	_, found = Find(start, "codefly.local.yaml", Ceilings)
	require.True(t, found, "without the repository bound the search crosses it")
}

// CODEFLY_CEILING_DIRECTORIES pins further roots, as GIT_CEILING_DIRECTORIES
// does: the search never enters one from below.
func TestNamedCeilingsAreNeverEntered(t *testing.T) {
	outer := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	touch(t, filepath.Join(outer, "workspace.codefly.yaml"))
	inner := filepath.Join(outer, "a", "b")
	require.NoError(t, os.MkdirAll(inner, 0o755))
	_, found := Find(inner, "workspace.codefly.yaml", Ceilings)
	require.True(t, found)

	t.Setenv(CeilingEnvironment, string(os.PathListSeparator)+outer)
	_, found = Find(inner, "workspace.codefly.yaml", Ceilings)
	require.False(t, found, "a named ceiling was entered")
	require.Equal(t, []string{inner, filepath.Join(outer, "a")}, Ancestors(inner, Ceilings))
}

// The starting directory is always read, even when it is itself a ceiling.
func TestTheStartIsAlwaysRead(t *testing.T) {
	start := t.TempDir()
	t.Setenv(CeilingEnvironment, start)
	touch(t, filepath.Join(start, "go.mod"))
	directory, found := Find(start, "go.mod", Repository)
	require.True(t, found)
	require.Equal(t, start, directory)
}
