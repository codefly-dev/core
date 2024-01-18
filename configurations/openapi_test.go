package configurations_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestOpenAPICombineForward(t *testing.T) {
	ctx := context.Background()

	endpoint := &configurations.Endpoint{Service: "org", Application: "management"}
	rest, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/swagger/one/org.swagger.json")
	assert.NoError(t, err)

	gateway := &configurations.Endpoint{Service: "api", Application: "public"}
	combinator, err := configurations.NewOpenAPICombinator(ctx, gateway, rest)
	assert.NoError(t, err)

	tmpDir := t.TempDir()
	defer os.RemoveAll(tmpDir)
	out := fmt.Sprintf("%s/openapi.json", tmpDir)
	combinator.WithDestination(out)
	combined, err := combinator.Combine(ctx)
	assert.NoError(t, err)

	content, _ := os.ReadFile(out)
	t.Log(string(content))

	// parse again
	api, err := configurations.ParseOpenAPI(content)
	assert.NoError(t, err)
	assert.NotNil(t, api)

	// Parse back and do some check
	result := configurations.EndpointRestAPI(combined)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Openapi)
	assert.Equal(t, 2, len(result.Routes))

	routes := map[string]configurations.RestRoute{
		"/management/org/version": {
			Service:     "org",
			Application: "management",
			Path:        "/management/org/version",
			Methods:     []configurations.HTTPMethod{configurations.HTTPMethodGet},
		},
		"/management/org/organization": {
			Service:     "org",
			Application: "management",
			Path:        "/management/org/version",
			Methods:     []configurations.HTTPMethod{configurations.HTTPMethodPost},
		},
	}
	for _, route := range result.Routes {
		t.Log("ROUTE", route)
		if expected, ok := routes[route.Path]; ok {
			assert.Equal(t, len(route.Methods), len(expected.Methods))
			continue
		}
		t.Errorf("missing route: %s", route.Path)
	}

}

func TestOpenAPICombineSample(t *testing.T) {
	ctx := context.Background()
	endpoint := &configurations.Endpoint{Service: "svc", Application: "app"}
	rest, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/swagger/sample/server.swagger.json")
	assert.NoError(t, err)

	otherEndpoint := &configurations.Endpoint{Service: "org", Application: "management"}
	otherRest, err := configurations.NewRestAPIFromOpenAPI(ctx, otherEndpoint, "testdata/swagger/sample/org.swagger.json")
	assert.NoError(t, err)

	gateway := &configurations.Endpoint{Service: "api", Application: "public"}
	combinator, err := configurations.NewOpenAPICombinator(ctx, gateway, rest, otherRest)
	assert.NoError(t, err)

	tmpDir := t.TempDir()
	defer os.RemoveAll(tmpDir)
	out := fmt.Sprintf("%s/openapi.json", tmpDir)
	combinator.WithDestination(out)
	combined, err := combinator.Combine(ctx)
	assert.NoError(t, err)

	// Parse back and do some check
	result := configurations.EndpointRestAPI(combined)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Openapi)
	assert.Equal(t, 3, len(result.Routes))

	desired := map[string]bool{"/app/svc/version": false, "/management/org/version": false, "/management/org/organization": false}
	for _, route := range result.Routes {
		if _, ok := desired[route.Path]; !ok {
			t.Errorf("unexpected route: %s", route.Path)
			continue
		}
		desired[route.Path] = true
	}
	for _, d := range desired {
		assert.True(t, d)
	}

}

func TestOpenAPICombineWithFilter(t *testing.T) {
	ctx := context.Background()
	endpoint := &configurations.Endpoint{Service: "svc", Application: "app"}
	rest, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/swagger/sample/server.swagger.json")
	assert.NoError(t, err)

	otherEndpoint := &configurations.Endpoint{Service: "org", Application: "management"}
	otherRest, err := configurations.NewRestAPIFromOpenAPI(ctx, otherEndpoint, "testdata/swagger/sample/org.swagger.json")
	assert.NoError(t, err)

	gateway := &configurations.Endpoint{Service: "api", Application: "public"}
	combinator, err := configurations.NewOpenAPICombinator(ctx, gateway, rest, otherRest)
	assert.NoError(t, err)
	combinator.Only(otherEndpoint.ServiceUnique(), "/organization")

	tmpDir := t.TempDir()
	defer os.RemoveAll(tmpDir)
	out := fmt.Sprintf("%s/openapi.json", tmpDir)
	combinator.WithDestination(out)
	combined, err := combinator.Combine(ctx)
	assert.NoError(t, err)

	// Parse back and do some check
	result := configurations.EndpointRestAPI(combined)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Openapi)
	assert.Equal(t, 2, len(result.Routes))

	desired := map[string]bool{"/app/svc/version": false, "/management/org/organization": false}
	for _, route := range result.Routes {
		if _, ok := desired[route.Path]; !ok {
			t.Errorf("unexpected route: %s", route.Path)
			continue
		}
		desired[route.Path] = true
	}
	for _, d := range desired {
		assert.True(t, d)
	}
}
