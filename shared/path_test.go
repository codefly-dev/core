package shared_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

func TestFileExists(t *testing.T) {
	p, err := shared.SolvePath("testdata/file.txt")
	require.NoError(t, err)
	require.True(t, shared.Must(shared.FileExists(context.Background(), p)))
}
