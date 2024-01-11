package shared_test

import (
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func TestIgnorePattern(t *testing.T) {
	ign := shared.NewIgnore("*.md")
	assert.True(t, ign.Skip("file.md"))
	assert.False(t, ign.Skip("somefile.txt"))
}