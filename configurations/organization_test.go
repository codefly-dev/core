package configurations_test

import (
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestValidOrganizationName(t *testing.T) {
	tcs := []struct {
		name  string
		valid bool
	}{
		{"codefly.ai", true},
		{"codefly", true},
		{"01codefly", false},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.valid, configurations.ValidOrganizationName(tc.name), tc.name)
		})
	}
}

func TestValidOrganizationDomain(t *testing.T) {
	tcs := []struct {
		name  string
		valid bool
	}{
		{"https://codefly.ai", true},
		{"https://codefly.ai/", true},
		{"codefly.ai/organization", false},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.valid, configurations.ValidOrganizationDomain(tc.name))
		})
	}
}
