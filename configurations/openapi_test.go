package configurations_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestOpenAPICombine(t *testing.T) {
	ctx := context.Background()
	endpoint := &configurations.Endpoint{Service: "svc", Application: "app"}
	rest, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/swagger/server.swagger.json")
	assert.NoError(t, err)

	otherEndpoint := &configurations.Endpoint{Service: "org", Application: "management"}
	otherRest, err := configurations.NewRestAPIFromOpenAPI(ctx, otherEndpoint, "testdata/swagger/org.swagger.json")
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
	rest, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/swagger/server.swagger.json")
	assert.NoError(t, err)

	otherEndpoint := &configurations.Endpoint{Service: "org", Application: "management"}
	otherRest, err := configurations.NewRestAPIFromOpenAPI(ctx, otherEndpoint, "testdata/swagger/org.swagger.json")
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
