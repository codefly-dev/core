package sbom

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPnpmSBOMRejectsVersionsThatCouldRunAProjectScript(t *testing.T) {
	for _, version := range []string{"", "garbage", "10.28.0", "11.0.0", "11.17.0-beta.1"} {
		require.ErrorIs(t, requirePnpmSBOMVersion(version), ErrUnsupported)
	}
	require.NoError(t, requirePnpmSBOMVersion("11.17.0\n"))
	require.NoError(t, requirePnpmSBOMVersion("12.6.0"))
	_, err := Pnpm(t.Context(), t.TempDir(), false)
	require.ErrorIs(t, err, ErrUnsupported)
}
