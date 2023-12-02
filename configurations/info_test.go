package configurations_test

import (
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestVersion(t *testing.T) {
	_, err := configurations.Version()
	assert.NoError(t, err)
}
