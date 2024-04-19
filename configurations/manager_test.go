package configurations_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/stretchr/testify/require"
)

func TestLoaderFlatLayout(t *testing.T) {
	testLoader(t, "testdata/flat")
}

func TestLoaderModuleLayout(t *testing.T) {
	testLoader(t, "testdata/module")
}

func testLoader(t *testing.T, dir string) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	loader, err := configurations.NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)

	manager, err := configurations.NewManager(ctx, workspace)
	require.NoError(t, err)

	manager.WithLoader(loader)

	env := resources.LocalEnvironment()

	require.NoError(t, manager.Load(ctx, env))

	confs, err := manager.GetConfigurations(ctx)
	require.NoError(t, err)
	for _, conf := range confs {
		fmt.Println(resources.MakeConfigurationSummary(conf))
	}
	//
	// - auth0/frontend
	// - global
	// app/ServiceWithModule
	// - something
	require.Equal(t, 3, len(confs))

	// Get  configuration value for some key

	conf, err := manager.GetConfiguration(ctx, "global")
	require.NoError(t, err)
	require.NotNil(t, conf)

	conf, err = manager.GetConfiguration(ctx, "auth0/frontend")
	require.NoError(t, err)
	require.NotNil(t, conf)

	conf, err = manager.GetConfiguration(ctx, "not-exist")
	require.NoError(t, err)
	require.Nil(t, conf)

	// For a service
	svc, err := workspace.FindUniqueServiceByName(ctx, "svc")
	require.NoError(t, err)
	conf, err = manager.GetServiceConfiguration(ctx, svc)
	require.NoError(t, err)
	require.NotNil(t, conf)

	// Get DNS for service and endpoint name
	dns, err := manager.GetDNS(ctx, svc, "rest")
	require.NoError(t, err)
	require.Equal(t, "localhost", dns.Host)
}
