package shared_test

import (
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestWarning(t *testing.T) {
	logger := shared.NewLogger("test")
	err := shared.NewUserWarning("This is a warning")
	assert.True(t, shared.IsUserWarning(err))
	assert.Equal(t, "This is a warning", shared.UserWarnMessage(err))

	err = logger.Wrapf(err, "This is a layer on top")
	assert.True(t, shared.IsUserWarning(err))
	assert.Equal(t, "This is a warning", shared.UserWarnMessage(err))

	err = shared.NewUserError("This is an error")
	assert.True(t, shared.IsUserError(err))
	assert.Equal(t, "This is an error", shared.UserErrorMessage(err))

	err = logger.Wrapf(err, "This is a layer on top")
	assert.True(t, shared.IsUserError(err))
	assert.Equal(t, "This is an error", shared.UserErrorMessage(err))

}
