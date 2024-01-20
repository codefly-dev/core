package configurations_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func TestOverrideFromContext(t *testing.T) {
	ctx := context.Background()
	override := shared.GetOverride(ctx)
	assert.Equal(t, shared.OverrideAll(), override)
}
