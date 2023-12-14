package configurations_test

import (
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

type Auth struct {
	Protected string `yaml:"protected"`
}

func TestRouteExtended(t *testing.T) {
	r, err := configurations.LoadExtendedRestRoute[Auth]("testdata/app/svc/rest.codefly.route.yaml", "app", "svc")
	assert.NoError(t, err)
	assert.Equal(t, "/test", r.Path)
	assert.Equal(t, "working", r.Extension.Protected)
}

func TestLoadingRoute(t *testing.T) {
	ctx := shared.NewContext()
	routes, err := configurations.LoadApplicationRoutes(ctx, "testdata")
	assert.NoError(t, err)
	assert.Equal(t, 1, len(routes))
	assert.Equal(t, "/test", routes[0].Path)
}

func TestLoadingExtendedRoute(t *testing.T) {
	ctx := shared.NewContext()
	routes, err := configurations.LoadApplicationExtendedRoutes[Auth](ctx, "testdata")
	assert.NoError(t, err)
	assert.Equal(t, 1, len(routes))
	assert.Equal(t, "/test", routes[0].Path)
	assert.Equal(t, "working", routes[0].Extension.Protected)
	unwrapped := configurations.UnwrapRoutes(routes)
	assert.Equal(t, 1, len(unwrapped))
}
