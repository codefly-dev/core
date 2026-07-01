package templates_test

import (
	"testing"

	"github.com/codefly-dev/core/generation"
	"github.com/codefly-dev/core/templates"
	"github.com/stretchr/testify/require"
)

// Overlapping replacements where one entry's output feeds a later entry's
// input must be applied in declaration order, deterministically.
func TestServiceReplacerAppliesInOrder(t *testing.T) {
	gen := &generation.Service{
		Replacements: []generation.Replacement{
			{From: "app", To: "myapp"},
			{From: "myapp", To: "acme"},
		},
	}
	replacer := templates.NewServiceReplacer(gen)
	for i := 0; i < 100; i++ {
		out, err := replacer.Do([]byte("app"))
		require.NoError(t, err)
		require.Equal(t, "acme", string(out))
	}
}
