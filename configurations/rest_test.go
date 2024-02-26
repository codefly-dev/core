package configurations_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

type Auth struct {
	Protected string `yaml:"protected"`
}

func Exhaust[T comparable](desired []*T, got []*T) error {
	lookup := make(map[T]bool)
	for _, d := range desired {
		lookup[*d] = false
	}
	for _, g := range got {
		lookup[*g] = true
	}
	for d, found := range lookup {
		if !found {
			return fmt.Errorf("expected %v not found", d)
		}
	}
	return nil
}

func TestRouteLoading(t *testing.T) {
	ctx := context.Background()
	r, err := configurations.LoadRestRouteGroup(ctx, "testdata/rests/app/svc/something-wicked.rest.codefly.yaml")
	assert.NoError(t, err)
	assert.Equal(t, "/something-wicked", r.Path)
	assert.Equal(t, 2, len(r.Routes))
	expected := []*configurations.RestRoute{
		{Path: "/something-wicked", Method: configurations.HTTPMethodGet},
		{Path: "/something-wicked", Method: configurations.HTTPMethodPost},
	}
	assert.NoError(t, Exhaust(expected, r.Routes))
}

type extendedRoute = configurations.ExtendedRestRoute[Auth]
type extendedRouteGroup = configurations.ExtendedRestRouteGroup[Auth]

func TestRouteExtended(t *testing.T) {
	ctx := context.Background()
	route := extendedRoute{RestRoute: configurations.RestRoute{Path: "/something-wicked"}}
	assert.Equal(t, route.Path, "/something-wicked")
	tmpDir := t.TempDir()
	defer os.RemoveAll(tmpDir)
	group := &extendedRouteGroup{Application: "app", Service: "svc", Path: "/something-wicked"}
	group.Add(route)
	group.Add(route) // Should not matter
	assert.Equal(t, 1, len(group.Routes))
	err := group.Save(ctx, tmpDir)
	assert.NoError(t, err)

	// Reload
	loader, err := configurations.NewExtendedRestRouteLoader[Auth](ctx, tmpDir)
	assert.NoError(t, err)

	err = loader.Load(ctx)
	assert.NoError(t, err)

	groups := loader.All()
	assert.Equal(t, 1, len(groups))
	group = loader.GroupFor("app/svc", "/something-wicked")
	assert.NotNil(t, group)
	assert.Equal(t, "/something-wicked", group.Path)
	assert.Equal(t, "app", group.Application)
	assert.Equal(t, "svc", group.Service)
	assert.Equal(t, 1, len(group.Routes))
}

func TestRouteExtendedLoading(t *testing.T) {
	ctx := context.Background()
	r, err := configurations.LoadExtendedRestRouteGroup[Auth](ctx, "testdata/rests/app/svc/something-wicked.rest.codefly.yaml")
	assert.NoError(t, err)
	expected := []*extendedRoute{
		{RestRoute: configurations.RestRoute{Path: "/something-wicked", Method: configurations.HTTPMethodGet}},
		{RestRoute: configurations.RestRoute{Path: "/something-wicked", Method: configurations.HTTPMethodPost}, Extension: Auth{Protected: "working"}},
	}
	assert.NoError(t, Exhaust(expected, r.Routes))
}

func TestLoadingRoute(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	loader, err := configurations.NewRestRouteLoader(ctx, "testdata/rests")
	assert.NoError(t, err)
	err = loader.Load(ctx)
	assert.NoError(t, err)
	routes := loader.All()
	assert.Equal(t, 2, len(routes))
	groups := loader.Groups()
	assert.Equal(t, 1, len(groups))

	groups = loader.GroupsFor("app/svc")
	assert.NotNil(t, groups)
	assert.Equal(t, 1, len(groups)) // One route
	group := groups[0]
	assert.Equal(t, "/something-wicked", group.Path)
	assert.Equal(t, 2, len(group.Routes))

	group = loader.GroupFor("app/svc", "/something-wicked")
	assert.NotNil(t, group)
	assert.Equal(t, "/something-wicked", group.Path)
	assert.Equal(t, "app", group.Application)
	assert.Equal(t, "svc", group.Service)
	assert.Equal(t, 2, len(group.Routes))

}

func TestLoadingExtendedRoute(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	loader, err := configurations.NewExtendedRestRouteLoader[Auth](ctx, "testdata/rests")
	assert.NoError(t, err)
	err = loader.Load(ctx)
	assert.NoError(t, err)
	routes := loader.All()
	assert.Equal(t, 2, len(routes))
	groups := loader.Groups()
	assert.NotNil(t, groups)
	assert.Equal(t, 1, len(groups)) // One route

	groups = loader.GroupsFor("app/svc")
	assert.NotNil(t, groups)
	assert.Equal(t, 1, len(groups)) // One route
	group := groups[0]
	assert.Equal(t, "/something-wicked", group.Path)
	assert.Equal(t, 2, len(group.Routes))

	group = loader.GroupFor("app/svc", "/something-wicked")
	assert.NotNil(t, group)
	assert.Equal(t, "/something-wicked", group.Path)
	assert.Equal(t, "app", group.Application)
	assert.Equal(t, "svc", group.Service)
	assert.Equal(t, 2, len(group.Routes))
}

func TestRestEnvironmentVariable(t *testing.T) {
	endpoint := &configurations.Endpoint{Application: "app", Service: "svc", Name: "api", Visibility: configurations.VisibilityPublic}
	route := &configurations.RestRoute{
		Path:   "/something-wicked",
		Method: configurations.HTTPMethodGet,
	}
	env := configurations.RestRoutesAsEnvironmentVariable(endpoint.Proto(), route.Proto())
	assert.Equal(t, "CODEFLY_RESTROUTE__APP__SVC___API____SOMETHING-WICKED_____GET=public", env)
}
