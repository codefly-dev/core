package providers_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/providers"
	"github.com/stretchr/testify/assert"
)

func TestFromService(t *testing.T) {
	service := &configurations.Service{
		Name:        "svc",
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

func TestLoadingProjectProviderInfoFromEnv(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	dir, err := shared.SolvePath("testdata")
	assert.NoError(t, err)
	ctx := context.Background()
	infos, err := providers.LoadProjectProviderFromDir(ctx, dir, configurations.Local())
	assert.NoError(t, err)
	assert.Len(t, infos, 2)
	info, err := configurations.FindProjectProvider("auth0/frontend", infos)
	assert.NoError(t, err)
	assert.Equal(t, "auth0/frontend", info.Name)
	assert.Equal(t, "client-id", info.Data["CLIENT_ID"])

	info, err = configurations.FindProjectProvider("global", infos)
	assert.NoError(t, err)
	assert.Equal(t, "global", info.Name)
	assert.Equal(t, "value", info.Data["key"])
}

func TestLoadingServiceProviderInfoFromEnv(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	dir, err := shared.SolvePath("testdata")
	assert.NoError(t, err)
	ctx := context.Background()
	infos, err := providers.LoadServiceProvidersFromDir(ctx, dir, configurations.Local())
	assert.NoError(t, err)
	assert.Len(t, infos, 1)
	info, err := configurations.FindServiceProvider("app/svc", "something", infos)
	assert.NoError(t, err)
	assert.Equal(t, "something", info.Name)
	assert.Equal(t, "true", info.Data["in_service"])
}

func TestLoadingProviderInfoFromEnv(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	dir, err := shared.SolvePath("testdata")
	assert.NoError(t, err)
	ctx := context.Background()
	infos, err := providers.LoadProviderFromEnvFiles(ctx, dir, configurations.Local())
	assert.NoError(t, err)
	assert.Len(t, infos, 3)
	info, err := configurations.FindProjectProvider("auth0/frontend", infos)
	assert.NoError(t, err)
	assert.Equal(t, "auth0/frontend", info.Name)
	assert.Equal(t, "client-id", info.Data["CLIENT_ID"])

	info, err = configurations.FindProjectProvider("global", infos)
	assert.NoError(t, err)
	assert.Equal(t, "global", info.Name)
	assert.Equal(t, "value", info.Data["key"])
	assert.Equal(t, "postgresql://user:password@localhost:30140/counter-python-nextjs-postgres?sslmode=disable", info.Data["another_key"])

	info, err = configurations.FindServiceProvider("app/svc", "something", infos)
	assert.NoError(t, err)
	assert.Equal(t, "something", info.Name)
	assert.Equal(t, "true", info.Data["in_service"])
}

func TestExtract(t *testing.T) {
	p := "applications/app/services/svc"
	out := providers.ExtractFromPath(p)
	assert.Equal(t, "app/svc", out)
}
