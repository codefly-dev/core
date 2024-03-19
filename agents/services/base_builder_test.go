package services_test

import (
	"testing"

	"github.com/codefly-dev/core/agents/services"
	"github.com/stretchr/testify/assert"
)

func TestSeveralEqualSign(t *testing.T) {
	env := "CODEFLY_PROVIDER__BACKEND__STORE___POSTGRES____CONNECTION=postgresql://user:password@host.docker.internal:42350/visitors"
	key, value, err := services.ToKeyAndValue(env)
	assert.NoError(t, err)
	assert.Equal(t, "CODEFLY_PROVIDER__BACKEND__STORE___POSTGRES____CONNECTION", key)
	assert.Equal(t, "postgresql://user:password@host.docker.internal:42350/visitors", value)
}
