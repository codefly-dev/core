package providers_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/codefly-dev/core/wool"

	"github.com/stretchr/testify/assert"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/providers"
)

func TestLoader(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	project, err := configurations.LoadProjectFromDir(ctx, "testdata")
	assert.NoError(t, err)

	loader, err := providers.NewConfigurationLocalReader(ctx, project)
	assert.NoError(t, err)

	configurationInfo, err := providers.NewConfigurationInformation(ctx, project)
	assert.NoError(t, err)

	configurationInfo.WithLoader(loader)

	env := configurations.Local()

	assert.NoError(t, configurationInfo.Load(ctx, env))

	confs, err := configurationInfo.GetConfigurations(ctx)
	assert.NoError(t, err)
	for _, conf := range confs {
		fmt.Println(configurations.MakeConfigurationSummary(conf))
	}
	// Project
	// - auth0/frontend
	// - global
	// app/ServiceWithApplication
	// - something
	assert.Equal(t, 3, len(confs))

	// Get Project configuration value for some key

	conf, err := configurationInfo.GetProjectConfiguration(ctx, "global")
	assert.NoError(t, err)
	assert.NotNil(t, conf)

	conf, err = configurationInfo.GetProjectConfiguration(ctx, "auth0/frontend")
	assert.NoError(t, err)
	assert.NotNil(t, conf)

	conf, err = configurationInfo.GetProjectConfiguration(ctx, "not-exist")
	assert.NoError(t, err)
	assert.Nil(t, conf)

	// For a service
	svc, err := configurations.LoadServiceFromDir(ctx, "testdata/applications/app/services/svc")
	assert.NoError(t, err)
	conf, err = configurationInfo.GetServiceConfiguration(ctx, svc)
	assert.NoError(t, err)
	assert.NotNil(t, conf)

}
