package resources_test

import (
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestParsingFromAddress(t *testing.T) {
	tcs := []struct {
		address  string
		hostname string
		port     uint16
	}{
		{"localhost:8080", "localhost", 8080},
		{"http://localhost:8080", "localhost", 8080},
	}
	for _, tc := range tcs {
		t.Run(tc.address, func(t *testing.T) {
			add, err := resources.ParseAddress(tc.address)
			require.NoError(t, err)
			require.Equal(t, tc.port, add.Port)
			require.Equal(t, tc.hostname, add.Hostname)
		})
	}
}
