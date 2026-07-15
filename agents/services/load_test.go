package services

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestBuilderLoadServiceHandlesCreationMode(t *testing.T) {
	ctx := context.Background()
	identity := saveLoadTestService(t, ctx)
	base := NewServiceBase(ctx, &resources.Agent{Kind: resources.ServiceAgent, Name: "test", Version: "0.0.1"})
	factory := fstest.MapFS{
		"templates/factory/GETTING_STARTED.md.tmpl": {Data: []byte("# {{ .Service.Name.Title }}\n")},
	}

	response, err := base.Builder.LoadService(ctx, &builderv0.LoadRequest{
		Identity:     identity,
		CreationMode: &builderv0.CreationMode{},
	}, BuilderLoad{Settings: &struct{}{}, FactoryTemplates: factory})
	require.NoError(t, err)
	require.Equal(t, builderv0.LoadStatus_READY, response.GetState().GetState(), response.GetState().GetMessage())
	require.Equal(t, "# ExampleService\n", response.GetGettingStarted())
}

func TestRuntimeLoadServiceLoadsEnvironmentAndEndpoints(t *testing.T) {
	ctx := context.Background()
	identity := saveLoadTestService(t, ctx)
	base := NewServiceBase(ctx, &resources.Agent{Kind: resources.ServiceAgent, Name: "test", Version: "0.0.1"})
	resolved := false

	response, err := base.Runtime.LoadService(ctx, &runtimev0.LoadRequest{
		Identity:    identity,
		Environment: &basev0.Environment{Name: "test"},
	}, RuntimeLoad{
		Settings: &struct{}{},
		ResolveEndpoints: func(_ context.Context, endpoints []*basev0.Endpoint) error {
			resolved = true
			require.Empty(t, endpoints)
			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, runtimev0.LoadStatus_READY, response.GetStatus().GetState())
	require.True(t, resolved)
	require.Equal(t, "test", base.Environment.GetName())
}

func saveLoadTestService(t *testing.T, ctx context.Context) *basev0.ServiceIdentity {
	t.Helper()
	workspace := t.TempDir()
	relative := filepath.Join("module", "example-service")
	service := &resources.Service{Name: "example-service", Version: "1.2.3"}
	require.NoError(t, service.SaveAtDir(ctx, filepath.Join(workspace, relative)))
	return &basev0.ServiceIdentity{
		Workspace:           "workspace",
		Module:              "module",
		Name:                "example-service",
		Version:             "1.2.3",
		WorkspacePath:       workspace,
		RelativeToWorkspace: relative,
	}
}
