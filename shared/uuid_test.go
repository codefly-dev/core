package shared_test

import (
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

func TestShortLowerUUID(t *testing.T) {
	seen := make(map[string]struct{})
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
	}
}
