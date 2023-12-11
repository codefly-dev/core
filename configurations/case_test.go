package configurations_test

import (
	"github.com/codefly-dev/core/shared"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTitleCase(t *testing.T) {
	tcs := []struct {
		input    string
		expected string
	}{
		{input: "name", expected: "Name"},
		{input: "some-name", expected: "SomeName"},
		{input: "some_name", expected: "SomeName"},
	}
	for _, tc := range tcs {
		actual := shared.ToTitle(tc.input)
		assert.Equal(t, tc.expected, actual)
	}
}
