package configurations_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/shared"

	"github.com/stretchr/testify/require"
)

func TestLoadingDirectoryFromFilesFlat(t *testing.T) {
	testLoadConfigurationsFromFiles(t, "testdata/flat")
}

func TestLoadingDirectoryFromFilesModules(t *testing.T) {
	testLoadConfigurationsFromFiles(t, "testdata/module")
}

func testLoadConfigurationsFromFiles(t *testing.T, dir string) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	dir, err := shared.SolvePath(dir)
	require.NoError(t, err)
	ctx := context.Background()
	infos, err := configurations.LoadConfigurationInformationsFromFiles(ctx, dir)
	require.NoError(t, err)
	// workspace
	// auth0/frontend global other_global
	// service
	// nested/other something
	require.Len(t, infos, 5)
}

func TestLocalLoaderFlatLayout(t *testing.T) {
	testLocalLoader(t, "testdata/flat")
}

func TestLocalLoaderModulesLayout(t *testing.T) {
	testLocalLoader(t, "testdata/module")
}

func testLocalLoader(t *testing.T, dir string) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	ws, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	loader, err := configurations.NewConfigurationLocalReader(ctx, ws)
	require.NoError(t, err)

	err = loader.Load(ctx, resources.LocalEnvironment())
	require.NoError(t, err)

	// config
	// global
	// - frontend
	// - global
	// - other_global
	// services
	// - svc1

	require.Equal(t, 4, len(loader.Configurations()))
	require.Equal(t, 2, len(loader.DNS()))

	dns := loader.DNS()[0]
	require.Equal(t, "localhost", dns.Host)
	require.Equal(t, uint32(8080), dns.Port)
	require.Equal(t, "rest", dns.Endpoint)
}

func TestFromService(t *testing.T) {
	service := &resources.Service{
		Name:   "ServiceWithModule",
		Module: "app",
	}
	tcs := []struct {
		in      string
		service string
		module  string
		name    string
	}{
		{in: "auth0", name: "auth0"},
		{in: "other_app/store:postgres", name: "postgres", service: "store", module: "other_app"},
		{in: "store:postgres", name: "postgres", service: "store", module: "app"},
	}

	for _, tc := range tcs {
		t.Run(tc.in, func(t *testing.T) {
			res, err := configurations.FromService(service, tc.in)
			require.NoError(t, err)
			require.Equal(t, res.Name, tc.name)
			if tc.service != "" {
				require.Equal(t, res.ServiceWithModule.Name, tc.service)
			}
			if tc.module != "" {
				require.Equal(t, res.ServiceWithModule.Module, tc.module)
			}
		})
	}
}

func TestExtract(t *testing.T) {
	p := "modules/app/services/ServiceWithModule"
	out := configurations.ExtractFromPath(p)
	require.Equal(t, "app/ServiceWithModule", out)
}
