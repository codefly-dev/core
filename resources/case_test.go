package resources_test

import (
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
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
		require.Equal(t, tc.expected, actual)
	}
}
