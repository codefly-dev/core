package providers_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/providers"
	"github.com/stretchr/testify/assert"
)

func TestLoadingDirectoryFromEnv(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	dir, err := shared.SolvePath("testdata")
	assert.NoError(t, err)
	ctx := context.Background()
	wrappers, err := providers.LoadConfigurationsFromEnvFiles(ctx, dir)
	assert.NoError(t, err)
	assert.Len(t, wrappers, 7)
}

func TestLocalLoaderFromEnv(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	project, err := configurations.LoadProjectFromDir(ctx, "testdata")
	assert.NoError(t, err)
	loader, err := providers.NewConfigurationLocalReader(ctx, project)
	assert.NoError(t, err)
	confs, err := loader.Load(ctx, configurations.Local())
	assert.NoError(t, err)
	assert.Equal(t, 3, len(confs))
}

func TestFromService(t *testing.T) {
	service := &configurations.Service{
		Name:        "ServiceWithApplication",
		Application: "app",
	}
	tcs := []struct {
		in          string
		service     string
		application string
		name        string
	}{
		{in: "auth0", name: "auth0"},
		{in: "other_app/store:postgres", name: "postgres", service: "store", application: "other_app"},
		{in: "store:postgres", name: "postgres", service: "store", application: "app"},
	}

	for _, tc := range tcs {
		t.Run(tc.in, func(t *testing.T) {
			res, err := providers.FromService(service, tc.in)
			assert.NoError(t, err)
			assert.Equal(t, res.Name, tc.name)
			if tc.service != "" {
				assert.Equal(t, res.ServiceWithApplication.Name, tc.service)
			}
			if tc.application != "" {
				assert.Equal(t, res.ServiceWithApplication.Application, tc.application)
			}
		})
	}
}

func TestExtract(t *testing.T) {
	p := "applications/app/services/ServiceWithApplication"
	out := providers.ExtractFromPath(p)
	assert.Equal(t, "app/ServiceWithApplication", out)
}
