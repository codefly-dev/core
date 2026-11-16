package resources

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A codefly.local.yaml above the repository a workspace lives in belongs to
// somebody else's checkout, or to whatever directory the repository was
// copied into. The overlay search reads up to the repository root and stops
// there, so a stray overlay in a parent directory never resolves this
// workspace's modules.
func TestTheLocalOverlayIsNeverReadFromAboveTheRepository(t *testing.T) {
	outer := t.TempDir()
	repository := filepath.Join(outer, "repository")
	workspace := filepath.Join(repository, "workspaces", "notes")
	require.NoError(t, os.MkdirAll(filepath.Join(repository, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outer, LocalOverlayConfigurationName),
		[]byte("resolve:\n    documents:\n        path: /elsewhere/documents\n"), 0o600))

	overlay, err := LoadLocalOverlay(t.Context(), workspace)
	require.NoError(t, err)
	require.Nil(t, overlay, "an overlay above the repository root was read")

	// Inside the repository it is still found, nearest first.
	require.NoError(t, os.WriteFile(filepath.Join(repository, LocalOverlayConfigurationName),
		[]byte("resolve:\n    documents:\n        path: ../documents\n"), 0o600))
	overlay, err = LoadLocalOverlay(t.Context(), workspace)
	require.NoError(t, err)
	require.NotNil(t, overlay, "the repository's own overlay is the workspace's")
}

// A workspace in a directory no repository holds — a test's temporary
// directory above all — reads no overlay from the shared temporary directory
// or above it.
func TestTheLocalOverlayIsNeverReadFromTheSharedTemporaryDirectory(t *testing.T) {
	shared := t.TempDir()
	t.Setenv("TMPDIR", shared)
	require.NoError(t, os.WriteFile(filepath.Join(shared, LocalOverlayConfigurationName),
		[]byte("resolve:\n    documents:\n        path: /elsewhere/documents\n"), 0o600))
	workspace, err := os.MkdirTemp(shared, "notes-")
	require.NoError(t, err)
	overlay, err := LoadLocalOverlay(t.Context(), workspace)
	require.NoError(t, err)
	require.Nil(t, overlay, "an overlay in the shared temporary directory was read")
}
