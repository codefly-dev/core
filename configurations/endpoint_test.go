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

func TestEndpointConventionEnv(t *testing.T) {
	tcs := []struct {
		endpoint *configurations.Endpoint
		key      string
	}{
		{&configurations.Endpoint{Application: "app", Service: "svc", API: configurations.Unknown}, "CODEFLY_ENDPOINT__APP__SVC"},
	}
	for _, tc := range tcs {
		t.Run(tc.key, func(t *testing.T) {
			assert.Equal(t, tc.key, configurations.EndpointEnvironmentVariableKey(tc.endpoint))
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
		expected *configurations.Endpoint
	}{
		{"app + svc", "app/svc", &configurations.Endpoint{Application: "app", Service: "svc", API: configurations.Unknown}},
		{"app + svc + endpoint", "app/svc/endpoint", &configurations.Endpoint{Application: "app", Service: "svc", Name: "endpoint", API: configurations.Unknown}},
		{"ap + svc + endpoint + api", "app/svc/endpoint::rest", &configurations.Endpoint{Application: "app", Service: "svc", Name: "endpoint", API: standards.REST}},
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

func TestEndpointLoadingFromDir(t *testing.T) {
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
