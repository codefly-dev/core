package configurations_test

import (
	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestIdentifier(t *testing.T) {
	tcs := []struct {
		identifier string
		app        string
		svc        string
		desired    string
	}{
		{"IDENTIFIER", "app", "svc", "CODEFLY_IDENTIFIER__APP__SVC"},
	}
	for _, tc := range tcs {
		t.Run(tc.desired, func(t *testing.T) {
			assert.Equal(t, tc.desired, configurations.IdentifierKey(tc.identifier, tc.app, tc.svc))
		})
	}
}
