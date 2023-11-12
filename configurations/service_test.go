package configurations_test

import (
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestParseServiceEntry(t *testing.T) {
	tests := []struct {
		input       string
		service     string
		application string
	}{
		{"app/svc", "svc", "app"},
		{"svc", "svc", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ref, err := configurations.ParseServiceInput(tt.input)
			assert.NoError(t, err)
			assert.Equalf(t, tt.service, ref.Name, "ParseServiceInput(%v) Unique failed", tt.input)
			assert.Equalf(t, tt.application, ref.Application, "ParseServiceInput(%v) Application failed", tt.input)
		})
	}
}
