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

type Extension struct {
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
	r, err := configurations.LoadRestRouteGroup(ctx, "testdata/routes/app/svc/something-wicked.route.codefly.yaml")
	assert.NoError(t, err)
	assert.Equal(t, "/something-wicked", r.Path)
	assert.Equal(t, 2, len(r.Routes))
	expected := []*configurations.RestRoute{
		{Path: "/something-wicked", Method: configurations.HTTPMethodGet},
		{Path: "/something-wicked", Method: configurations.HTTPMethodPost},
	}
	assert.NoError(t, Exhaust(expected, r.Routes))
}

type extendedRoute = configurations.ExtendedRestRoute[Extension]
type extendedRouteGroup = configurations.ExtendedRestRouteGroup[Extension]

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
	loader, err := configurations.NewExtendedRouteLoader[Extension](ctx, tmpDir)
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
	r, err := configurations.LoadExtendedRestRoute[Extension]("testdata/routes/app/svc/something-wicked.route.codefly.yaml")
	assert.NoError(t, err)
	expected := []*extendedRoute{
		{RestRoute: configurations.RestRoute{Path: "/something-wicked", Method: configurations.HTTPMethodGet}},
		{RestRoute: configurations.RestRoute{Path: "/something-wicked", Method: configurations.HTTPMethodPost}, Extension: Extension{Protected: "working"}},
	}
	assert.NoError(t, Exhaust(expected, r.Routes))
}

func TestLoadingRoute(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	loader, err := configurations.NewRouteLoader(ctx, "testdata/routes")
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
	loader, err := configurations.NewExtendedRouteLoader[Extension](ctx, "testdata/routes")
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
