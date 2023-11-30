package configurations_test

import (
	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
	"testing"
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
		actual := configurations.ToTitle(tc.input)
		assert.Equal(t, tc.expected, actual)
	}
}
