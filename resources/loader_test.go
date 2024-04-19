package resources_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

func TestOverrideFromContext(t *testing.T) {
	ctx := context.Background()
	override := shared.GetOverride(ctx)
	require.Equal(t, shared.OverrideAll(), override)
}
