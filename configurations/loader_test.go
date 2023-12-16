package configurations_test

import (
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func TestOverrideFromContext(t *testing.T) {
	ctx := shared.NewContext()
	override := shared.GetOverride(ctx)
	assert.Equal(t, shared.SilentOverride(), override)
}
