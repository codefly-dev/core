package shared_test

import (
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func TestFileExists(t *testing.T) {
	p, err := shared.SolvePath("testdata/file.txt")
	assert.NoError(t, err)
	assert.True(t, shared.FileExists(p))
}
