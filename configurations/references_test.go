package configurations_test

import (
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestParseServiceReference(t *testing.T) {
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
			ref, err := configurations.ParseServiceReference(tt.input)
			assert.NoError(t, err)
			assert.Equalf(t, tt.service, ref.Name, "ParseServiceReference(%v) MakeUnique failed", tt.input)
			assert.Equalf(t, tt.application, ref.Application, "ParseServiceReference(%v) Application failed", tt.input)
		})
	}
}
