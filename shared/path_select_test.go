package shared_test

import (
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func TestIgnorePattern(t *testing.T) {
	ign := shared.NewIgnore("*.md")
	assert.False(t, ign.Keep("file.md"))
	assert.True(t, ign.Keep("somefile.txt"))
}

func TestSelectPattern(t *testing.T) {
	ign := shared.NewSelect("*.md")
	assert.True(t, ign.Keep("file.md"))
	assert.False(t, ign.Keep("somefile.txt"))
}
