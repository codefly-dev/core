package configurations_test

import (
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestPortFromAddress(t *testing.T) {
	tcs := []struct {
		address string
		port    int
	}{
		{"localhost:8080", 8080},
		{"http://localhost:8080/tcp", 8080},
		{"grp://localhost:8080", 8080},
	}
	for _, tc := range tcs {
		t.Run(tc.address, func(t *testing.T) {
			port, err := configurations.PortFromAddress(tc.address)
			assert.NoError(t, err)
			assert.Equal(t, tc.port, port)
		})
	}
}
