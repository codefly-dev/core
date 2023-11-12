package configurations_test

import (
	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestParsing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		key      string
		expected string
	}{
		{"app + svc", "CODEFLY_ENDPOINT__APP__SVC", "app/svc"},
		{"app + svc + api", "CODEFLY_ENDPOINT__APP__SVC____REST", "app/svc::rest"},
		{"app + svc + endpoint", "CODEFLY_ENDPOINT__APP__SVC___ENDPOINT", "app/svc/endpoint"},
		{"app. svc +endpoint+api", "CODEFLY_ENDPOINT__APP__SVC___ENDPOINT____REST", "app/svc/endpoint::rest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unique, err := configurations.ParseEndpointEnvironmentVariableKey(tc.key)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, unique)
		})
	}
}

func TestUniqueAndBack(t *testing.T) {
	unique := "app/svc/cool::rest"
	e := &configurations.Endpoint{Name: "cool", Api: configurations.Rest}
	assert.Equal(t, unique, e.Unique("app", "svc"))
	key := configurations.AsEndpointEnvironmentVariableKey("app", "svc", e)
	back, err := configurations.ParseEndpointEnvironmentVariableKey(key)
	assert.NoError(t, err)
	assert.Equal(t, unique, back)

	unique = "app/svc::rest"
	e = &configurations.Endpoint{Name: configurations.Rest, Api: configurations.Rest}
	key = configurations.AsEndpointEnvironmentVariableKey("app", "svc", e)
	back, err = configurations.ParseEndpointEnvironmentVariableKey(key)
	assert.NoError(t, err)
	assert.Equal(t, unique, back)
}