package shared_test

import (
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

func TestShortLowerUUID(t *testing.T) {
	seen := make(map[string]struct{})
	firstChars := make(map[byte]struct{})
	for i := 0; i < 1000; i++ {
		id, err := shared.ShortLowerUUID()
		require.NoError(t, err)
		require.Len(t, id, 10)
		for _, c := range id {
			require.True(t, c >= 'a' && c <= 'z', "unexpected character %q", c)
		}
		_, dup := seen[id]
		require.False(t, dup, "duplicate id %q generated after %d iterations", id, i)
		seen[id] = struct{}{}
		firstChars[id[0]] = struct{}{}
	}
	// The full 128 bits must reach the high-order base26 digits. If only the
	// low 32 bits were used (the pre-fix bug), every id would start with 'a'.
	require.Greater(t, len(firstChars), 1, "leading character never varies — high-order entropy is being dropped")
}
