package resources_test

import (
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestParseServiceReference(t *testing.T) {
	tests := []struct {
		input   string
		service string
		module  string
	}{
		{"app/svc", "svc", "app"},
		{"svc", "svc", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ref, err := resources.ParseServiceReference(tt.input)
			require.NoError(t, err)
			require.Equalf(t, tt.service, ref.Name, "ParseServiceReference(%v) MakeUnique failed", tt.input)
			require.Equalf(t, tt.module, ref.Module, "ParseServiceReference(%v) Module failed", tt.input)
		})
	}
}
