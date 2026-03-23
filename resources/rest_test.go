package resources_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/stretchr/testify/require"
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
	r, err := resources.LoadRestRouteGroup(ctx, "testdata/rests/app/svc/something-wicked.rest.codefly.yaml")
	require.NoError(t, err)
	require.Equal(t, "/something-wicked", r.Path)
	require.Equal(t, 2, len(r.Routes))
	expected := []*resources.RestRoute{
		{Path: "/something-wicked", Method: resources.HTTPMethodGet},
		{Path: "/something-wicked", Method: resources.HTTPMethodPost},
	}
	require.NoError(t, Exhaust(expected, r.Routes))
}

type extendedRoute = resources.ExtendedRestRoute[Auth]
type extendedRouteGroup = resources.ExtendedRestRouteGroup[Auth]

func TestRouteExtended(t *testing.T) {
	ctx := context.Background()
	route := extendedRoute{RestRoute: resources.RestRoute{Path: "/something-wicked"}}
	require.Equal(t, route.Path, "/something-wicked")
	tmpDir := t.TempDir()
	defer os.RemoveAll(tmpDir)
	group := &extendedRouteGroup{Module: "app", Service: "svc", Path: "/something-wicked"}
	group.Add(route)
	group.Add(route) // Should not matter
	require.Equal(t, 1, len(group.Routes))
	err := group.Save(ctx, tmpDir)
	require.NoError(t, err)

	// Reload
	loader, err := resources.NewExtendedRestRouteLoader[Auth](ctx, tmpDir)
	require.NoError(t, err)

	err = loader.Load(ctx)
	require.NoError(t, err)

	groups := loader.All()
	require.Equal(t, 1, len(groups))
	group = loader.GroupFor("app/svc", "/something-wicked")
	require.NotNil(t, group)
	require.Equal(t, "/something-wicked", group.Path)
	require.Equal(t, "app", group.Module)
	require.Equal(t, "svc", group.Service)
	require.Equal(t, 1, len(group.Routes))
}

func TestRouteExtendedLoading(t *testing.T) {
	ctx := context.Background()
	r, err := resources.LoadExtendedRestRouteGroup[Auth](ctx, "testdata/rests/app/svc/something-wicked.rest.codefly.yaml")
	require.NoError(t, err)
	expected := []*extendedRoute{
		{RestRoute: resources.RestRoute{Path: "/something-wicked", Method: resources.HTTPMethodGet}},
		{RestRoute: resources.RestRoute{Path: "/something-wicked", Method: resources.HTTPMethodPost}, Extension: Auth{Protected: "working"}},
	}
	require.NoError(t, Exhaust(expected, r.Routes))
}

func TestLoadingRoute(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	loader, err := resources.NewRestRouteLoader(ctx, "testdata/rests")
	require.NoError(t, err)
	err = loader.Load(ctx)
	require.NoError(t, err)
	routes := loader.All()
	require.Equal(t, 2, len(routes))
	groups := loader.Groups()
	require.Equal(t, 1, len(groups))

	groups = loader.GroupsFor("app/svc")
	require.NotNil(t, groups)
	require.Equal(t, 1, len(groups)) // One route
	group := groups[0]
	require.Equal(t, "/something-wicked", group.Path)
	require.Equal(t, 2, len(group.Routes))

	group = loader.GroupFor("app/svc", "/something-wicked")
	require.NotNil(t, group)
	require.Equal(t, "/something-wicked", group.Path)
	require.Equal(t, "app", group.Module)
	require.Equal(t, "svc", group.Service)
	require.Equal(t, 2, len(group.Routes))

}

func TestLoadingExtendedRoute(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	loader, err := resources.NewExtendedRestRouteLoader[Auth](ctx, "testdata/rests")
	require.NoError(t, err)
	err = loader.Load(ctx)
	require.NoError(t, err)
	routes := loader.All()
	require.Equal(t, 2, len(routes))
	groups := loader.Groups()
	require.NotNil(t, groups)
	require.Equal(t, 1, len(groups)) // One route

	groups = loader.GroupsFor("app/svc")
	require.NotNil(t, groups)
	require.Equal(t, 1, len(groups)) // One route
	group := groups[0]
	require.Equal(t, "/something-wicked", group.Path)
	require.Equal(t, 2, len(group.Routes))

	group = loader.GroupFor("app/svc", "/something-wicked")
	require.NotNil(t, group)
	require.Equal(t, "/something-wicked", group.Path)
	require.Equal(t, "app", group.Module)
	require.Equal(t, "svc", group.Service)
	require.Equal(t, 2, len(group.Routes))
}

func TestRestEnvironmentVariable(t *testing.T) {
	// TODO
	//endpoint := &configurations.Endpoint{Module: "app", Service: "svc", Name: "api", Visibility: configurations.VisibilityPublic}
	//endpoint.WithDefault()
	//route := &configurations.RestRoute{
	//	Path:   "/something-wicked",
	//	Method: configurations.HTTPMethodGet,
	//}
	//env := configurations.RestRoutesAsEnvironmentVariable(shared.Must(endpoint.Proto()), shared.Must(route.Proto()))
	//require.Equal(t, "CODEFLY_RESTROUTE__APP__SVC___API____SOMETHING-WICKED_____GET=public", env)
}
