package configurations_test

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"

	"github.com/stretchr/testify/assert"

	"github.com/codefly-dev/core/configurations"
)

func TestAPILoading(t *testing.T) {
	ctx := context.Background()
	endpoint := &configurations.Endpoint{Application: "app", Service: "svc"}
	e, err := configurations.NewRestAPIFromOpenAPI(ctx, endpoint, "testdata/swagger/one/org.swagger.json")
	assert.NoError(t, err)
	rest := configurations.EndpointRestAPI(e)
	assert.NotNil(t, rest)
	assert.Equal(t, 2, len(rest.Groups)) // 2 Paths
	var routes []*basev0.RestRoute
	for _, group := range rest.Groups {
		routes = append(routes, group.Routes...)
	}
	assert.Equal(t, 3, len(routes)) // 3 Routes (1 path with 2 Methods)
}
