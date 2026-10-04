//go:build pnpm_required

package sbom

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestPnpmInventoriesTheLockWithoutInstallation(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS("testdata/pnpm")))
	before, err := os.ReadFile(filepath.Join(dir, "pnpm-lock.yaml"))
	require.NoError(t, err)
	prod, err := Pnpm(t.Context(), dir, false)
	require.NoError(t, err)
	require.Len(t, prod.Bom.Components, 1)
	require.Equal(t, "is-number", prod.Bom.Components[0].Name)
	require.Equal(t, "7.0.0", prod.Bom.Components[0].Version)
	all, err := Pnpm(t.Context(), dir, true)
	require.NoError(t, err)
	require.Len(t, all.Bom.Components, 3, "development closure includes is-odd and its own is-number version")
	replay, err := Pnpm(t.Context(), dir, false)
	require.NoError(t, err)
	require.Equal(t, prod.SHA256, replay.SHA256, "timestamps must not alter canonical evidence")
	after, err := os.ReadFile(filepath.Join(dir, "pnpm-lock.yaml"))
	require.NoError(t, err)
	require.Equal(t, before, after)
	_, err = os.Stat(filepath.Join(dir, "node_modules"))
	require.True(t, os.IsNotExist(err))
	_, err = Pnpm(t.Context(), t.TempDir(), false)
	require.ErrorIs(t, err, ErrUnsupported)
}
