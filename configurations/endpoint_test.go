package configurations_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/codefly-dev/core/configurations/standards"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestLoadEndpoints(t *testing.T) {
	ctx := context.Background()
	service, err := configurations.LoadServiceFromDir(ctx, "testdata/endpoints/basic")
	assert.NoError(t, err)
	endpoints, err := service.LoadEndpoints(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(endpoints))
}

// TODO: MOVE THE ENV VARIABLES SOMEWHERE ELSE
func TestEndpointConventionEnv(t *testing.T) {
	tcs := []struct {
		info *configurations.EndpointInformation
		key  string
	}{
		{&configurations.EndpointInformation{Application: "app", Service: "svc", API: configurations.Unknown}, "CODEFLY_ENDPOINT__APP__SVC"},
	}
	for _, tc := range tcs {
		t.Run(tc.key, func(t *testing.T) {
			assert.Equal(t, tc.key, configurations.EndpointEnvironmentVariableKey(tc.info))
		})
	}
}

func TestSerializeAddressesSerialization(t *testing.T) {
	tcs := []struct {
		addresses []string
	}{
		{[]string{"a", "b", "c"}},
	}
	for _, tc := range tcs {
		t.Run(strings.Join(tc.addresses, " "), func(t *testing.T) {
			ser := configurations.SerializeAddresses(tc.addresses)
			des, err := configurations.DeserializeAddresses(ser)
			assert.NoError(t, err)
			assert.True(t, reflect.DeepEqual(tc.addresses, des))
		})
	}
}

func TestEndpointUniqueParsing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		unique   string
		expected *configurations.EndpointInformation
	}{
		{"app + svc", "app/svc", &configurations.EndpointInformation{Application: "app", Service: "svc", API: configurations.Unknown}},
		{"app + svc + info", "app/svc/info", &configurations.EndpointInformation{Application: "app", Service: "svc", Name: "info", API: configurations.Unknown}},
		{"ap + svc + info + api", "app/svc/info::rest", &configurations.EndpointInformation{Application: "app", Service: "svc", Name: "info", API: standards.REST}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := configurations.ParseEndpoint(tc.unique)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected.Name, e.Name)
			assert.Equal(t, tc.expected.Application, e.Application)
			assert.Equal(t, tc.expected.Service, e.Service)
			assert.Equal(t, tc.expected.API, e.API)
		})
	}
}

//
//func TestEndpointSwaggerChange(t *testing.T) {
//	ctx := context.Background()
//	endpoint := &configurations.Endpoint{Application: "app", Service: "svc", Name: "rest"}
//	endpoint.WithDefault()
//
//	e, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/endpoints/swagger/original.swagger.json")
//	assert.NoError(t, err)
//	hash, err := configurations.EndpointHash(ctx, e)
//	assert.NoError(t, err)
//
//	// Removed path swagger
//	e, err = configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/endpoints/swagger/path_removed.swagger.json")
//	assert.NoError(t, err)
//	updatedHash, err := configurations.EndpointHash(ctx, e)
//	assert.NoError(t, err)
//	assert.NotEqual(t, hash, updatedHash)
//
//	// Changed path swagger
//	e, err = configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/endpoints/swagger/path_name_changed.swagger.json")
//	assert.NoError(t, err)
//	updatedHash, err = configurations.EndpointHash(ctx, e)
//	assert.NoError(t, err)
//	assert.NotEqual(t, hash, updatedHash)
//}
