//go:build pnpm_required

package audit

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestPnpmAuditsTheLockWithoutInstallation(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS("../sbom/testdata/pnpm")))
	before, err := os.ReadFile(filepath.Join(dir, "pnpm-lock.yaml"))
	require.NoError(t, err)
	for _, includeDev := range []bool{false, true} {
		result, err := PnpmWithOptions(t.Context(), dir, NodeOptions{IncludeDevDependencies: includeDev, IncludeOutdated: true})
		require.NoError(t, err)
		require.Contains(t, result.Tool, "pnpm-audit")
	}
	after, err := os.ReadFile(filepath.Join(dir, "pnpm-lock.yaml"))
	require.NoError(t, err)
	require.Equal(t, before, after)
	_, err = os.Stat(filepath.Join(dir, "node_modules"))
	require.True(t, os.IsNotExist(err))
}
