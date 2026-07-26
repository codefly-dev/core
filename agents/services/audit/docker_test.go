package audit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTrivyCacheLockSerializesAgentProcesses(t *testing.T) {
	cache := t.TempDir()

	first, err := lockTrivyCache(context.Background(), cache)
	require.NoError(t, err)

	waitContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = lockTrivyCache(waitContext, cache)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.NoError(t, first.Unlock())
	second, err := lockTrivyCache(context.Background(), cache)
	require.NoError(t, err)
	require.NoError(t, second.Unlock())
}
