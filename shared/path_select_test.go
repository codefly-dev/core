package shared_test

import (
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

func TestIgnorePattern(t *testing.T) {
	ign := shared.NewIgnore("*.md")
	require.False(t, ign.Keep("file.md"))
	require.True(t, ign.Keep("somefile.txt"))
}

func TestSelectPattern(t *testing.T) {
	ign := shared.NewSelect("*.md")
	require.True(t, ign.Keep("file.md"))
	require.False(t, ign.Keep("somefile.txt"))
}
