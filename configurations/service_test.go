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
		{"applications", "applications", ""},
		{"applications@codefly", "applications", "codefly"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			service, app, _ := configurations.ParseServiceInput(tt.input)
			assert.Equalf(t, tt.service, service, "ParseServiceInput(%v) Unique failed", tt.input)
			assert.Equalf(t, tt.application, app, "ParseServiceInput(%v) Application failed", tt.input)
		})
	}
}
