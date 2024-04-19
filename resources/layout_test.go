package resources_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/resources"
)

func TestFlatLayout(t *testing.T) {
	ctx := context.Background()
	_, err := resources.NewFlatLayout(ctx, "root", nil)
	require.NoError(t, err)
}
