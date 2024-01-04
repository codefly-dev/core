package configurations_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/configurations/standards"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
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
		{"ap + svc + endpoint + api", "CODEFLY_ENDPOINT__APP__SVC___ENDPOINT____REST", "app/svc/endpoint::rest"},
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
	e := &configurations.Endpoint{Name: "cool", API: standards.REST, Application: "app", Service: "svc"}
	assert.Equal(t, unique, e.Unique())
	key := configurations.EndpointEnvironmentVariableKey(e)
	back, err := configurations.ParseEndpointEnvironmentVariableKey(key)
	assert.NoError(t, err)
	assert.Equal(t, unique, back)

	unique = "app/svc/rest"
	e = &configurations.Endpoint{Name: standards.REST, API: standards.REST, Application: "app", Service: "svc"}
	key = configurations.EndpointEnvironmentVariableKey(e)
	back, err = configurations.ParseEndpointEnvironmentVariableKey(key)
	assert.NoError(t, err)
	assert.Equal(t, unique, back)

	unique = "app/svc"
	e = &configurations.Endpoint{Application: "app", Service: "svc"}
	key = configurations.EndpointEnvironmentVariableKey(e)
	back, err = configurations.ParseEndpointEnvironmentVariableKey(key)
	assert.NoError(t, err)
	assert.Equal(t, unique, back)
}

func TestLoadingFromDir(t *testing.T) {
	ctx := context.Background()
	conf, err := configurations.LoadServiceFromDirUnsafe(ctx, "testdata/service")
	assert.NoError(t, err)

	assert.Equal(t, 2, len(conf.Endpoints))

	var restFound bool
	var grpcFound bool
	for _, e := range conf.Endpoints {
		if e.Name == standards.REST {
			restFound = true
			assert.Equal(t, "application", e.Visibility)
		}
		if e.Name == standards.GRPC {
			grpcFound = true
			assert.Equal(t, "", e.Visibility)
		}
	}
	assert.True(t, restFound)
	assert.True(t, grpcFound)
}
