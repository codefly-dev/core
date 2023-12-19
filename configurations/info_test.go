package configurations_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestVersion(t *testing.T) {
	_, err := configurations.Version(context.Background())
	assert.NoError(t, err)
}
