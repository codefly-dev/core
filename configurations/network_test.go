package configurations_test

import (
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
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
			add, err := configurations.ParseAddress(tc.address)
			assert.NoError(t, err)
			assert.Equal(t, tc.port, add.Port)
			assert.Equal(t, tc.hostname, add.Hostname)
		})
	}
}
