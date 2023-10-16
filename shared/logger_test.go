package shared_test

import (
	"github.com/hygge-io/hygge/pkg/core"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestWarning(t *testing.T) {
	logger := core.NewLogger("test")
	err := core.NewUserWarning("This is a warning")
	assert.True(t, core.IsUserWarning(err))
	assert.Equal(t, "This is a warning", core.UserWarnMessage(err))

	err = logger.Wrapf(err, "This is a layer on top")
	assert.True(t, core.IsUserWarning(err))
	assert.Equal(t, "This is a warning", core.UserWarnMessage(err))

	err = core.NewUserError("This is an error")
	assert.True(t, core.IsUserError(err))
	assert.Equal(t, "This is an error", core.UserErrorMessage(err))

	err = logger.Wrapf(err, "This is a layer on top")
	assert.True(t, core.IsUserError(err))
	assert.Equal(t, "This is an error", core.UserErrorMessage(err))

}
