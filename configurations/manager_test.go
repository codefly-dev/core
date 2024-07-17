package configurations_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/shared"

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

	confs, err := manager.GetWorkspaceConfigurations(ctx)
	require.NoError(t, err)
	// - auth0/frontend
	// - global
	// - other_global
	require.Equal(t, 3, len(confs))

	// Get  configuration value for some key
	conf, err := resources.FindWorkspaceConfiguration(ctx, confs, "global")
	require.NoError(t, err)
	require.NotNil(t, conf)
	require.Equal(t, "value", shared.Must(resources.GetConfigurationValue(ctx, conf, "global", "key")))

	confs, err = manager.GetServiceConfigurations(ctx)

	require.NoError(t, err)
	// mod/ServiceWithModule
	// - something
	require.Equal(t, 1, len(confs))

	// For a service
	svc, err := workspace.FindUniqueServiceByName(ctx, "svc")
	require.NoError(t, err)

	identity, err := svc.Identity()
	require.NoError(t, err)

	conf, err = manager.GetServiceConfiguration(ctx, identity)
	require.NoError(t, err)
	require.NotNil(t, conf)

	// Get DNS for service and endpoint name
	dns, err := manager.GetDNS(ctx, identity, "rest")
	require.NoError(t, err)
	require.Equal(t, "localhost", dns.Host)

	// Get DNS for service and endpoint name
	svc2, err := workspace.FindUniqueServiceByName(ctx, "svc2")
	require.NoError(t, err)

	identity2, err := svc2.Identity()
	require.NoError(t, err)

	dns, err = manager.GetDNS(ctx, identity2, "rest")
	require.NoError(t, err)
	require.Equal(t, "aws.magic", dns.Host)
}
