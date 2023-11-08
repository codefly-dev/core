package configurations_test

import (
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
	"testing"
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

func TestLoading(t *testing.T) {
	routes, err := configurations.LoadApplicationRoutes("testdata", shared.NewLogger("test"))
	assert.NoError(t, err)
	assert.Equal(t, 1, len(routes))
	assert.Equal(t, "/test", routes[0].Path)
}

func TestLoadingExtended(t *testing.T) {
	routes, err := configurations.LoadApplicationExtendedRoutes[Auth]("testdata", shared.NewLogger("test"))
	assert.NoError(t, err)
	assert.Equal(t, 1, len(routes))
	assert.Equal(t, "/test", routes[0].Path)
	assert.Equal(t, "working", routes[0].Extension.Protected)
	unwrapped := configurations.UnwrapRoutes(routes)
	assert.Equal(t, 1, len(unwrapped))
}
